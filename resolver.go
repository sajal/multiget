package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/miekg/dns"
	"io/ioutil"
	"runtime"
	"strconv"
	"time"
)

type dnsresponse struct {
	response *dns.Msg
	server   string
}

type config struct {
	Servers []string
	Port    int
}

var servers []string //Global variable for list of dns servers
/*
type dnsrequest struct {
	query   dnsresponse
	channel chan dnsresponse
}
var service chan dnsrequest

func queryservice(request chan dnsrequest) {
	c := new(dns.Client)
	for {
		select {
		case req := <-request:
			var reply dnsresponse
			in, _, err := c.Exchange(req.query.response, req.query.server)
			if err != nil {
				fmt.Printf("booboo (%s)%s\n", req.query.server, err)
				reply.response = nil
			} else {
				reply.response = in
			}
			reply.server = req.query.server
			req.channel <- reply
		}
	}
}
*/
func singlequery(query *dns.Msg, server string, resp chan dnsresponse) {
	//start := time.Now()
	c := new(dns.Client)
	in, _, err := c.Exchange(query, server)
	var reply dnsresponse
	if err != nil {
		fmt.Printf("booboo (%s)%s\n", server, err)
		reply.response = nil
	} else {
		reply.response = in
	}
	reply.server = server
	resp <- reply
}

func multidns(query *dns.Msg, out chan *dns.Msg) {
	start := time.Now()
	response := make(chan dnsresponse, len(servers))
	for _, server := range servers {
		go singlequery(query, server, response)
		//var req dnsrequest
		//req.query.response = query
		//req.query.server = server
		//req.channel = response
		//service <- req
	}

	var logline string
	sent := false
	for i := 0; i < len(servers); i++ {
		result := <-response
		if !sent && result.response != nil {
			//We have a winner...
			//Send only one response. query is satisfied. now dick around to collect more stats
			out <- result.response
			sent = true
			//Log with special flag. May not always be the first to answer...
			logline += fmt.Sprintf("%s (%s - W) ; ", result.server, time.Since(start))
		} else {
			//... but log everyone
			logline += fmt.Sprintf("%s (%s) ; ", result.server, time.Since(start))
		}
	}
	//logline += "\n"
	var name []string
	for _, q := range query.Question {
		name = append(name, q.Name+":"+dns.Class(q.Qclass).String())
	}
	fmt.Printf("%s (%s): %s\n", start, name, logline)
	//fmt.Printf("Exiting...\n")
}

type cacheobj struct {
	ans    []dns.RR
	expire time.Time
}

//var cache map[dns.Question]cacheobj

func iscachable(r *dns.Msg) bool {
	if len(r.Question) == 1 {
		if r.Question[0].Qtype == 1 {
			//This here is cachable
			//fmt.Printf("%v\n", r.Question[0])
			return true
		}
	}
	return false
}

type cacherequest struct {
	msg *dns.Msg
	channel  chan []dns.RR
}

type putrequest struct {
	question dns.Question
	result   []dns.RR
	ttl      uint32
}

var cachereq chan cacherequest
var cacheput chan putrequest

func cacheservice(req chan cacherequest, putter chan putrequest) {
	cache := make(map[dns.Question]cacheobj)
	for {
		select {
		case request := <-req:
			obj, exists := cache[request.msg.Question[0]]
			if exists {
				request.channel <- obj.ans
				//Check if stale
				if obj.expire.Before(time.Now()){
					fmt.Printf("Stale\n")
					go runquery(request.msg, true)
				}
			} else {
				request.channel <- nil
			}
		case put := <-putter:
			cache[put.question] = cacheobj{ans: put.result, expire: time.Now().Add(time.Duration(put.ttl) * time.Second)}
		}
	}

}

func putincache(r *dns.Msg) {
	if iscachable(r) {
		//fmt.Printf("%s\n", r.Answer)
		var minttl uint32
		var newans []dns.RR
		for _, ans := range r.Answer {
			//fmt.Printf("%s\n", ans.Header().Ttl)
			if minttl == 0 || minttl > ans.Header().Ttl {
				minttl = ans.Header().Ttl
			}
			if ans.Header().Rrtype == dns.TypeA {
				line := fmt.Sprintf("%s %d IN A %s", r.Question[0].Name, minttl, ans.(*dns.A).A )
				rr, _ := dns.NewRR( line )
				//fmt.Println(rr)
				newans = append(newans, rr)

			}
		}
		cacheput <- putrequest{question: r.Question[0], ttl: minttl, result: newans}
		//Cache forever for now.
		//cache[r.Question[0]] = cacheobj{ans: r.Answer, expire: time.Now()}
		//fmt.Printf("Cachable for %d\n", minttl)
	}
}

func runquery(r *dns.Msg, forcemiss bool) *dns.Msg {
	c := make(chan *dns.Msg, 1)
	//Send out query unmolested

	if iscachable(r) && !forcemiss{
		resp := make(chan []dns.RR, 1)
		cachereq <- cacherequest{channel: resp, msg: r}
		ans := <-resp
		if ans != nil {
			fmt.Printf("EXISTS IN CACHE\n")
			r.Answer = ans
			return r

		}
	}

	go multidns(r, c)
	in := <-c
	go putincache(in)
	//Write first response unmolested
	return in

}

func handleRequest(w dns.ResponseWriter, r *dns.Msg) {
	w.WriteMsg(runquery(r, false))
}

func main() {
	//Just for fun..lets make it multicore...
	numCPU := runtime.NumCPU()
	fmt.Printf("Launching %v procs\n", numCPU)
	runtime.GOMAXPROCS(numCPU)
	var conffile string
	cachereq = make(chan cacherequest, 100)
	cacheput = make(chan putrequest, 100)
	go cacheservice(cachereq, cacheput)
	flag.StringVar(&conffile, "config", "None", "path to config file")
	flag.Parse()
	var conf config
	if conffile == "None" {
		//Fallback using Google and OpenDNS
		fmt.Printf("Not using config file\n")
		conf.Servers = []string{"8.8.8.8:53", "8.8.4.4:53", "208.67.222.222:53", "208.67.220.220:53"}
		conf.Port = 53
	} else {
		fmt.Printf("using %s as config file\n", conffile)
		content, err := ioutil.ReadFile(conffile)
		if err != nil {
			panic(err)
		}
		fmt.Println(string(content))
		err = json.Unmarshal(content, &conf)
		if err != nil {
			panic(err)
		}
	}
	servers = conf.Servers
	//initialize the services
	/*
		service = make(chan dnsrequest, 100)
		for i := 0; i < 10; i++ {
			go queryservice(service)
		}
	*/
	fmt.Printf("Going to listen on %d\n", conf.Port)
	server := &dns.Server{Addr: ":" + strconv.Itoa(conf.Port), Net: "udp"}
	dns.HandleFunc(".", handleRequest)
	err := server.ListenAndServe() //Blocking .. forever..
	if err != nil {
		panic(err)
	}

}

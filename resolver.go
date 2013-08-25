package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/miekg/dns"
	"io/ioutil"
	"log"
	"net"
	"os"
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

const (
	ICMP_ECHO_REQUEST = 8
	ICMP_ECHO_REPLY   = 0
)

// returns a suitable 'ping request' packet, with id & seq and a
// payload length of pktlen
func makePingRequest(id, seq, pktlen int, filler []byte) []byte {
	p := make([]byte, pktlen)
	copy(p[8:], bytes.Repeat(filler, (pktlen-8)/len(filler)+1))

	p[0] = ICMP_ECHO_REQUEST // type
	p[1] = 0                 // code
	p[2] = 0                 // cksum
	p[3] = 0                 // cksum
	p[4] = uint8(id >> 8)    // id
	p[5] = uint8(id & 0xff)  // id
	p[6] = uint8(seq >> 8)   // sequence
	p[7] = uint8(seq & 0xff) // sequence

	// calculate icmp checksum
	cklen := len(p)
	s := uint32(0)
	for i := 0; i < (cklen - 1); i += 2 {
		s += uint32(p[i+1])<<8 | uint32(p[i])
	}
	if cklen&1 == 1 {
		s += uint32(p[cklen-1])
	}
	s = (s >> 16) + (s & 0xffff)
	s = s + (s >> 16)

	// place checksum back in header; using ^= avoids the
	// assumption the checksum bytes are zero
	p[2] ^= uint8(^s & 0xff)
	p[3] ^= uint8(^s >> 8)

	return p
}

func singleping(ip string) (time.Duration, error) {
	addr := net.IPAddr{IP: net.ParseIP(ip)}
	sendid := os.Getpid() & 0xffff
	sendseq := 1
	pingpktlen := 64
	sendpkt := makePingRequest(sendid, sendseq, pingpktlen, []byte("Go Ping"))
	ipconn, err := net.DialIP("ip4:icmp", nil, &addr) // *IPConn (Conn 인터페이스 구현)
	if err != nil {
		log.Fatalf(`net.DialIP("ip4:icmp", %v) = %v`, ipconn, err)
	}
	ipconn.SetDeadline(time.Now().Add(time.Second)) //1 Second timeout
	start := time.Now()
	n, err := ipconn.WriteToIP(sendpkt, &addr)
	if err != nil || n != pingpktlen {
		log.Fatalf(`net.WriteToIP(..., %v) = %v, %v`, addr, n, err)
	}

	resp := make([]byte, 1024)
	_, _, pingerr := ipconn.ReadFrom(resp)
	/*
		if pingerr != nil {
			fmt.Printf("%s : FAIL\n", ip)
		} else {
			fmt.Printf("%s : %s\n", ip, time.Since(start))
		}
	*/
	// log.Printf("%x", resp)

	return time.Since(start), pingerr
}

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
	var allresults []*dns.Msg
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
		//extract ips from result.response
		if result.response != nil {
			if iscachable(result.response) {
				for _, ans := range result.response.Answer {
					if ans.Header().Rrtype == dns.TypeA {
						//fmt.Printf("%s\n", ans.(*dns.A).A)
						ipchan <- fmt.Sprintf("%s", ans.(*dns.A).A)
					}
				}
				allresults = append(allresults, result.response)
			}
		}
	}
	//logline += "\n"
	var name []string
	for _, q := range query.Question {
		name = append(name, q.Name+":"+dns.Class(q.Qclass).String())
	}
	fmt.Printf("%s (%s): %s\n", start, name, logline)
	if len(allresults) > 0 {
		putincache(allresults)
	}
	//fmt.Printf("Exiting...\n")
}

type cacheobj struct {
	ans    []dns.RR
	expire time.Time
}

//var cache map[dns.Question]cacheobj

var ipchan chan string

type ipresult struct {
	timing      time.Duration
	status      bool
	lastchecked time.Time
}

type updateres struct {
	ip     string
	result ipresult
}

func pingtest(ip string, cb chan updateres) {
	timing, err := singleping(ip)
	if err != nil {
		cb <- updateres{ip: ip, result: ipresult{status: false, lastchecked: time.Now()}}
	} else {
		cb <- updateres{ip: ip, result: ipresult{status: true, timing: timing, lastchecked: time.Now()}}
	}

}

type getbestrequest struct {
	candidates []string    //List of possible ips
	channel    chan string //The best ip. Blank if none.
}

var getbest chan getbestrequest

func iptracker(incoming chan string, getbest chan getbestrequest) {
	ips := make(map[string]ipresult)
	resultupdate := make(chan updateres, 100)
	for {
		select {
		case ip := <-incoming:
			//fmt.Println(ip)
			_, exists := ips[ip]
			if !exists {
				//Start non-blocking test
				go pingtest(ip, resultupdate)
				//fmt.Printf("Length %d\n", len(ips))
			}
		case result := <-resultupdate:
			//All writes serialized
			ips[result.ip] = result.result
		case bestreq := <-getbest:
			best := ""
			besttime := time.Minute
			for _, candidate := range bestreq.candidates {
				result, exists := ips[candidate]
				if exists {
					if result.status {
						if besttime > result.timing {
							besttime = result.timing
							best = candidate
						}
					}
				}
			}
			bestreq.channel <- best
		case <-time.After(time.Second * 10):
			//Dump onscreen every 10 secs
			for ip, res := range ips {
				fmt.Printf("Resultlog %s : %v : %s : %s\n", ip, res.status, res.timing, res.lastchecked)
			}
		case <-time.After(time.Second):
			//Pingtest the oldest ip
			oldest := ""
			oldesttime := time.Now()
			for ip, res := range ips {
				//fmt.Printf("Resultlog %s : %v : %s : %s\n", ip, res.status, res.timing, res.lastchecked)
				if res.lastchecked.Before(oldesttime) {
					oldest = ip
					oldesttime = res.lastchecked
				}
			}
			if oldest != "" {
				fmt.Printf("Recheck: %s\n", oldest)
				//				fmt.Printf("Resultlog %s : %v : %s : %s\n", ip, res.status, res.timing, res.lastchecked)
				go pingtest(oldest, resultupdate)
			}
		}
	}
}

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
	msg     *dns.Msg
	channel chan []dns.RR
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
				var allips []string
				var best string
				for _, a := range obj.ans {
					allips = append(allips, fmt.Sprintf("%s", a.(*dns.A).A))
				}
				//fmt.Printf("%s\n", allips)
				if len(allips) > 0 {
					c := make(chan string, 1)
					getbest <- getbestrequest{candidates: allips, channel: c}
					best = <- c
					fmt.Printf("Best: %s\n", best)
				}
				if best != "" {
					//we actually have a winner
					line := fmt.Sprintf("%s %d IN A %s", request.msg.Question[0].Name, 60, best)
					rr, _ := dns.NewRR(line)
					request.channel <- []dns.RR{rr}
				} else {
					//Somehow we dont have a "best" answer
					request.channel <- obj.ans
				}
				//Check if stale
				if obj.expire.Before(time.Now()) {
					//If a stale object was served, make fresh response.
					fmt.Printf("Stale\n")
					go runquery(request.msg, true)
				}
			} else {
				request.channel <- nil
			}
		case put := <-putter:
			fmt.Printf("Inserting %s : %d\n", put.question, len(put.result))
			cache[put.question] = cacheobj{ans: put.result, expire: time.Now().Add(time.Duration(put.ttl) * time.Second)}
		}
	}

}

func putincache(results []*dns.Msg) {
	var newans []dns.RR
	var minttl uint32
	var question dns.Question
	for _, r := range results {
		if iscachable(r) {
			//fmt.Printf("%s\n", r.Answer)
			question = r.Question[0]
			for _, ans := range r.Answer {
				//fmt.Printf("%s\n", ans.Header().Ttl)
				if minttl == 0 || minttl > ans.Header().Ttl {
					minttl = ans.Header().Ttl
				}
				if ans.Header().Rrtype == dns.TypeA {
					line := fmt.Sprintf("%s %d IN A %s", r.Question[0].Name, minttl, ans.(*dns.A).A)
					rr, _ := dns.NewRR(line)
					//fmt.Println(rr)
					newans = append(newans, rr)
				}
			}
			//Cache forever for now.
			//cache[r.Question[0]] = cacheobj{ans: r.Answer, expire: time.Now()}
			//fmt.Printf("Cachable for %d\n", minttl)
		}
	}
	fmt.Printf("Putting %d\n", len(newans))
	if len(newans) > 0 {
		cacheput <- putrequest{question: question, ttl: minttl, result: newans}
	}
}

func runquery(r *dns.Msg, forcemiss bool) *dns.Msg {
	c := make(chan *dns.Msg, 1)
	//Send out query unmolested

	if iscachable(r) && !forcemiss {
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
	//go putincache(in)
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
	ipchan = make(chan string, 100)
	getbest = make(chan getbestrequest, 100)
	go iptracker(ipchan, getbest)
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

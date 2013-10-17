package main

import (
	"fmt"
	"github.com/miekg/dns"
	"time"
)

func handleRequest(w dns.ResponseWriter, r *dns.Msg) {
	start := time.Now()
	c := new(dns.Client)
	in, _, err := c.Exchange(r, "8.8.8.8:53")
	if err != nil {
		fmt.Printf("booboo (%v): %s\n", r.Question, err)
	} else {
		w.WriteMsg(in)
	}
	fmt.Printf("%v, %s, %v\n", time.Now(), time.Since(start), r.Question)

}

func main() {
	dns.HandleFunc(".", handleRequest)
	server := &dns.Server{Addr: ":53", Net: "udp"}

	err := server.ListenAndServe() //Blocking .. forever..
	if err != nil {
		panic(err)
	}
}

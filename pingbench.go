package main

//Shamelessly stollen from https://github.com/atomaths/gtug8/blob/master/ping/ping.go
//Function to take a bunch of ips, and determine the least latent ip to connect to...
//Root or GTFO
//Does single ping request

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"os"
	"time"
)

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
	if pingerr != nil {
		fmt.Printf("%s : FAIL\n", ip)
	} else {
		fmt.Printf("%s : %s\n", ip, time.Since(start))
	}

	// log.Printf("%x", resp)

	return time.Since(start), pingerr
}

func main() {
	fmt.Printf("Hello\n")
	ips := []string{"63.251.19.10", "66.151.47.236", "202.58.141.130", "69.88.149.144", "74.201.53.198", "77.242.195.172", "77.242.195.167", "95.172.71.48", "202.58.15.224", "69.88.149.139", "95.172.71.41", "69.88.149.138", "203.190.126.14", "202.58.15.228", "66.151.147.240", "95.172.71.53", "202.58.141.130", "69.88.149.135", "95.172.71.44", "77.242.195.175", "95.172.71.40", "95.172.71.49"}
	for _, ip := range ips {
		singleping(ip)
		//time.Sleep(time.Second)
	}
	//go singleping("66.151.47.236")
	//go singleping("202.58.141.130")
	<-time.After(time.Second * time.Duration(3)) //Wait 3 secs and exit
}

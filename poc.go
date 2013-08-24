package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

func fetch(path, host string, res chan string) {
	start := time.Now()
	r := rand.New(rand.NewSource(start.UnixNano()))
	url := "http://" + host + path + "?" + strconv.Itoa(r.Int()) //Random Querystring to burst isp cache
	fmt.Printf("Downloading: %s\n", url)
	response, err := http.Get(url)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s response ready : %s\n", host, time.Since(start))
	defer response.Body.Close()
	contents, err := ioutil.ReadAll(response.Body)
	fmt.Printf("%s response read : %s\n", host, time.Since(start))
	select { //Select with default makes the send non-blocking
	case res <- string(contents):
	default:
	}
	fmt.Printf("%s Finish: %s\n", time.Since(start), url)
}

func fetcher(path string, hosts []string) string {
	// Simulate download of a path from multiple backends
	result := make(chan string, 1)
	for _, host := range hosts {
		go fetch(path, host, result)
	}
	return <-result
}

func multifetch(path string) string {
	hosts := []string{"www.cdnplanet.com", "netdna.cdnplanet.com", "ec.cdnplanet.com", "internap1.cdnplanet.com", "fastly.cdnplanet.com", "cc.cdnplanet.com", "bg.cdnplanet.com"}
	return fetcher(path, hosts)
}

func main() {
	var path string
	flag.StringVar(&path, "path", "/static/uploads/tbtest.txt", "path to download")
	flag.Parse()
	fmt.Printf("Response length : %d\n", len(multifetch(path)))
	<-make(chan bool) //Block forever
}

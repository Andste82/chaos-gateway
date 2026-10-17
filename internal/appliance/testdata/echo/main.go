// Command echo answers every HTTP request with its tag and the address the request came from. The level 2
// test (M35a) runs it as the device behind a port forward, in the client namespace on the host, and inside
// a container that publishes a port on the gateway VM: a static binary, so it runs in any image.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
)

func main() {
	listen := flag.String("listen", ":8000", "address to listen on")
	tag := flag.String("tag", "echo", "the first word of every answer")
	flag.Parse()
	log.Fatal(http.ListenAndServe(*listen, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		fmt.Fprintf(w, "%s client=%s\n", *tag, host)
	})))
}

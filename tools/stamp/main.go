// Command stamp writes or reads the connection stamp on an agent binary. CI
// uses it to check that stamped binaries still run on every platform.
//
//	stamp -in agent -out stamped -server https://support.example.com -code 123456789
//	stamp -read stamped
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/photoncody/simpleremote/internal/stamp"
)

func main() {
	in := flag.String("in", "", "agent binary to stamp")
	out := flag.String("out", "", "where to write the stamped binary")
	server := flag.String("server", "https://support.example.com", "server URL")
	code := flag.String("code", "123456789", "session code")
	read := flag.String("read", "", "print the stamp of this file and exit")
	flag.Parse()

	if *read != "" {
		s, err := stamp.ReadFile(*read)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%+v\n", s)
		return
	}
	if *in == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	base, err := os.ReadFile(*in)
	if err != nil {
		log.Fatal(err)
	}
	st := stamp.Stamp{ServerURL: *server, Code: *code}
	var data []byte
	if bytes.HasPrefix(base, []byte("MZ")) {
		// Windows: same signature-preserving path the server uses.
		trailer, err := stamp.Trailer(st)
		if err != nil {
			log.Fatal(err)
		}
		if data, err = stamp.AppendPE(base, trailer); err != nil {
			log.Fatal(err)
		}
	} else {
		var buf bytes.Buffer
		if err := stamp.Write(&buf, base, st); err != nil {
			log.Fatal(err)
		}
		data = buf.Bytes()
	}
	if err := os.WriteFile(*out, data, 0o755); err != nil {
		log.Fatal(err)
	}
}

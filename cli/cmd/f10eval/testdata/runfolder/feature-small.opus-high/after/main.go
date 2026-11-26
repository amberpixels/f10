// Command greet prints a greeting.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func greeting(name string) string {
	return "Hello, " + name + "!"
}

func main() {
	name := flag.String("name", "world", "who to greet")
	shout := flag.Bool("shout", false, "greet in capitals")
	flag.Parse()

	msg := greeting(*name)
	if *shout {
		msg = strings.ToUpper(msg)
	}

	if _, err := fmt.Fprintln(os.Stdout, msg); err != nil {
		os.Exit(1)
	}
}

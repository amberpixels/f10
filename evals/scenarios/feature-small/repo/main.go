// Command greet prints a greeting.
package main

import (
	"flag"
	"fmt"
	"os"
)

func greeting(name string) string {
	return "Hello, " + name + "!"
}

func main() {
	name := flag.String("name", "world", "who to greet")
	flag.Parse()

	if _, err := fmt.Fprintln(os.Stdout, greeting(*name)); err != nil {
		os.Exit(1)
	}
}

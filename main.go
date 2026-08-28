package main

import (
	"fmt"
	"os"

	"github.com/mr0xb/rampart/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rampart:", err)
		os.Exit(1)
	}
}

package main

import (
	"bytes"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: crasher <input>")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	if bytes.Contains(data, []byte("CRASH")) {
		panic("crash token")
	}
	fmt.Println("ok")
}

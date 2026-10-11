package main

import (
	"fmt"

	"example.com/demo/multi"
	"example.com/demo/named"
	"example.com/demo/single"
	"example.com/demo/single2"
)

func main() {
	fmt.Println(single.One(), single2.Two(), multi.A(), multi.B(), named.Named(), named.Other())
}

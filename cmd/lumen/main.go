package main

import (
	"os"

	"lumen/internal/lumen"
)

func main() {
	os.Exit(lumen.Run(os.Args[1:]))
}

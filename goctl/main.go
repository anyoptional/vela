package main

import (
	"fmt"
	"os"

	"github.com/zeromicro/go-zero/tools/goctl/plugin"
)

func main() {
	payload, err := plugin.NewPlugin()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := gen(payload.Api, payload.Dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

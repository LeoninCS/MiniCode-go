// Command minicode 是 MiniCode-go 项目的 CLI 入口。
package main

import (
	"os"

	"github.com/MiniCode-go/minicode/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

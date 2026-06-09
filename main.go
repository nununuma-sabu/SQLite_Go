package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stdout, "Must supply a database filename.")
		os.Exit(int(ExitFailure))
	}

	table, err := dbOpen(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stdout, "Unable to open file")
		os.Exit(int(ExitFailure))
	}

	// runの戻り値をそのままOSプロセスの終了コードにする。
	os.Exit(int(run(os.Stdin, os.Stdout, table)))
}

package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stdout, "Usage: sqlite-go <database> [sql-file]")
		os.Exit(int(ExitFailure))
	}

	table, err := dbOpen(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stdout, "Unable to open file")
		os.Exit(int(ExitFailure))
	}

	if len(os.Args) == 3 {
		file, err := os.Open(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stdout, "Unable to open SQL file")
			if closeErr := dbClose(table); closeErr != nil {
				fmt.Fprintf(os.Stdout, "Error closing db file: %v\n", closeErr)
			}
			os.Exit(int(ExitFailure))
		}
		defer file.Close()

		os.Exit(int(runSQLScript(file, os.Stdout, table)))
	}

	// runの戻り値をそのままOSプロセスの終了コードにする。
	os.Exit(int(run(os.Stdin, os.Stdout, table)))
}

package main

import (
	"fmt"
	"os"
)

// main is the program entrypoint. Its only job is to delegate to run and
// translate the returned error into a non-zero exit code, keeping all real
// logic in a testable function.
func main() {
	if err := run(); err != nil {
		fmt.Println("error running rienda: " + err.Error())
		os.Exit(1)
	}
}

// run contains the actual program logic. Unlike main, it returns an error
// instead of calling os.Exit, which makes it easier to test. It receives any
// values from main's environment (args, etc) as parameters.
func run() error {
	return nil
}

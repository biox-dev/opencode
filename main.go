package main

import (
	"github.com/biox-dev/opencode/cmd"
	"github.com/biox-dev/opencode/internal/logging"
)

func main() {
	defer logging.RecoverPanic("main", func() {
		logging.ErrorPersist("Application terminated due to unhandled panic")
	})

	cmd.Execute()
}

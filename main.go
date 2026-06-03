package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

func runCmd(name string, arg ...string) {
	cmd := exec.Command(name, arg...)
	cmd.Stdout = os.Stdout
	cmd.Run()
}

func clear() {
	switch runtime.GOOS {
	case "darwin":
		runCmd("clear")
	case "linux":
		runCmd("clear")
	case "windows":
		runCmd("cmd", "/c", "cls")
	default:
		runCmd("clear")
	}
}

func print(msg string) {
	fmt.Println(msg)
}

func sleep(milliseconds int) {
	time.Sleep(time.Duration(milliseconds) * time.Millisecond)
}

// --------------------------------------------------
// --------------------------------------------------

var stop = false

func main() {
	clear()
	print("====================")
	print("|      ARANEA      |")
	print("====================")
	print("")
	print("pre-alpha v0")

	for !stop {
		print("tetst")
		sleep(500)
	}

}

func init() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		stop = true
		clear()
		print("shutting down...")
		os.Exit(0)
	}()
}

package main

import "log"

// main 只做一件事：执行 Cobra 根命令。
func main() {
	if err := newRootCmd().Execute(); err != nil {
		log.Fatal(err)
	}
}

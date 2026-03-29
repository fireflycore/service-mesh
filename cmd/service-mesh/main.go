package main

import "log"

// main 只做一件事：执行 Cobra 根命令。
func main() {
	if err := newRootCmd().Execute(); err != nil {
		// Cobra 已经负责把用户可读错误输出到 stderr，
		// 这里再用 Fatal 结束进程并返回非零退出码。
		log.Fatal(err)
	}
}

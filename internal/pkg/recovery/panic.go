package recovery

import (
	"fmt"
	"log"
	"runtime"
	"runtime/debug"
)

func WithRecover(name string, fn func()) func() {
	return func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				pc, file, line, ok := runtime.Caller(4)

				var funcName string
				if ok {
					funcName = runtime.FuncForPC(pc).Name()
				} else {
					funcName = "unknown"
				}

				log.Printf("PANIC RECOVERED in goroutine '%s':\n"+
					"  Function: %s\n"+
					"  Location: %s:%d\n"+
					"  Error: %v\n"+
					"  Stack Trace:\n%s\n",
					name, funcName, file, line, r, string(stack))
			}
		}()
		fn()
	}
}

func WithRecoverAndRestart(name string, fn func()) func() {
	return func() {
		for {
			func() {
				defer func() {
					if r := recover(); r != nil {
						stack := debug.Stack()
						pc, file, line, ok := runtime.Caller(5)

						var funcName string
						if ok {
							funcName = runtime.FuncForPC(pc).Name()
						} else {
							funcName = "unknown"
						}

						log.Printf("PANIC RECOVERED in goroutine '%s' (will restart):\n"+
							"  Function: %s\n"+
							"  Location: %s:%d\n"+
							"  Error: %v\n"+
							"  Stack Trace:\n%s\n",
							name, funcName, file, line, r, string(stack))
					}
				}()
				fn()
			}()

			log.Printf("Goroutine '%s' exited normally, not restarting", name)
			break
		}
	}
}

func WithRecoverCallback(name string, fn func(), onPanic func(error)) func() {
	return func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				pc, file, line, ok := runtime.Caller(4)

				var funcName string
				if ok {
					funcName = runtime.FuncForPC(pc).Name()
				} else {
					funcName = "unknown"
				}

				panicErr := fmt.Errorf("panic in goroutine '%s' at %s:%d in %s: %v\nStack:\n%s",
					name, file, line, funcName, r, string(stack))

				log.Printf("PANIC RECOVERED: %v", panicErr)

				if onPanic != nil {
					onPanic(panicErr)
				}
			}
		}()
		fn()
	}
}

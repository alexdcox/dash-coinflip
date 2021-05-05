package main

import (
	"fmt"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func panicIfErr(err error) {
	if err != nil {
		panicErr(err)
	}
}

func panicErr(err error) {
	logrus.Panic(StackTracerMessage(err))
}

func StackTracerMessage(err error) string {
	var errString string

	if err != nil {
		errString = "ERROR: " + err.Error()

		if stackTracer, isStackTracer := err.(StackTracer); isStackTracer {
			errString += "\n"
			for _, f := range stackTracer.StackTrace() {
				errString += fmt.Sprintf("%+v\n", f)
			}
		}
	}

	return errString
}

type StackTracer interface {
	StackTrace() errors.StackTrace
}

package logger

import (
	"fmt"
	"log"
	"os"
)

type Logger interface {
	Debug(msg string, args ...interface{})
	Info(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Fatal(msg string, args ...interface{})
}

type logger struct {
	std  *log.Logger
	level string
}

func New(level string) Logger {
	return &logger{
		std:  log.New(os.Stdout, "", log.Ldate|log.Ltime),
		level: level,
	}
}

func (l *logger) Debug(msg string, args ...interface{}) {
	if l.shouldLog("debug") {
		l.log("DEBUG", msg, args...)
	}
}

func (l *logger) Info(msg string, args ...interface{}) {
	if l.shouldLog("info") {
		l.log("INFO", msg, args...)
	}
}

func (l *logger) Warn(msg string, args ...interface{}) {
	if l.shouldLog("warn") {
		l.log("WARN", msg, args...)
	}
}

func (l *logger) Error(msg string, args ...interface{}) {
	if l.shouldLog("error") {
		l.log("ERROR", msg, args...)
	}
}

func (l *logger) Fatal(msg string, args ...interface{}) {
	l.log("FATAL", msg, args...)
	os.Exit(1)
}

func (l *logger) log(level, msg string, args ...interface{}) {
	formatted := msg
	if len(args) > 0 {
		formatted = l.formatMessage(msg, args...)
	}
	l.std.Printf("[%s] %s", level, formatted)
}

func (l *logger) formatMessage(msg string, args ...interface{}) string {
	result := msg
	for i := 0; i < len(args); i += 2 {
		if i+1 < len(args) {
			key := fmt.Sprintf("%v", args[i])
			value := fmt.Sprintf("%v", args[i+1])
			result += fmt.Sprintf(" | %s=%s", key, value)
		}
	}
	return result
}

func (l *logger) shouldLog(level string) bool {
	levels := map[string]int{
		"debug": 0,
		"info":  1,
		"warn":  2,
		"error": 3,
		"fatal": 4,
	}
	
	currentLevel, ok := levels[l.level]
	if !ok {
		currentLevel = 1
	}
	
	msgLevel, ok := levels[level]
	if !ok {
		msgLevel = 1
	}
	
	return msgLevel >= currentLevel
}

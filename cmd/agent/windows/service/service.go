// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build windows
// +build windows

package service

import (
	"fmt"
	"os"
	"time"

	"github.com/DataDog/datadog-agent/cmd/agent/app"
	"github.com/DataDog/datadog-agent/cmd/agent/common"
	"github.com/DataDog/datadog-agent/cmd/agent/common/signals"
	"github.com/DataDog/datadog-agent/pkg/config"
	"github.com/DataDog/datadog-agent/pkg/util/log"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
)

var logFile *os.File

func writeLogFile(s string) {
	if logFile != nil {
		logFile.WriteString(s + "\n")
		logFile.Sync()
	}
}

func init() {
	var err error
	logFile, err = os.OpenFile(`C:\ProgramData\Datadog\logs\service.txt`, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0660)
	fmt.Println(err)
	writeLogFile("Init")
}

var elog debug.Log

type agentWindowsService struct{}

// Execute sets up the configuration and runs the Agent as a Windows service
func (m *agentWindowsService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	defer func() {
		if r := recover(); r != nil {
			writeLogFile("Recovered")
			writeLogFile(fmt.Sprintf("%v", r))
		}
		logFile.Close()
	}()
	writeLogFile("Execute")
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown
	changes <- svc.Status{State: svc.StartPending}
	writeLogFile("svc.StartPending")
	if err := common.CheckAndUpgradeConfig(); err != nil {
		elog.Warning(0x80000002, err.Error())
		// continue running with what we have.
	}
	writeLogFile("CheckAndUpgradeConfig")
	if err := app.StartAgent(); err != nil {
		writeLogFile("StartAgent")
		log.Errorf("Failed to start agent %v", err)
		elog.Error(0xc000000B, err.Error())
		errno = 1 // indicates non-successful return from handler.
		changes <- svc.Status{State: svc.Stopped}
		return
	}
	elog.Info(0x40000003, config.ServiceName)
	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
				// Testing deadlock from https://code.google.com/p/winsvc/issues/detail?id=4
				time.Sleep(100 * time.Millisecond)
				changes <- c.CurrentStatus
			case svc.Stop:
				log.Info("Received stop message from service control manager")
				elog.Info(0x4000000c, config.ServiceName)
				break loop
			case svc.PreShutdown:
				log.Info("Received pre shutdown message from service control manager")
				elog.Info(0x40000010, config.ServiceName)
				break loop
			case svc.Shutdown:
				log.Info("Received shutdown message from service control manager")
				elog.Info(0x4000000d, config.ServiceName)
				break loop
			default:
				log.Warnf("unexpected control request #%d", c)
				elog.Error(0xc0000009, fmt.Sprint(c.Cmd))
			}
		case <-signals.Stopper:
			elog.Info(0x4000000a, config.ServiceName)
			break loop

		}
	}
	elog.Info(0x4000000d, config.ServiceName)
	log.Infof("Initiating service shutdown")
	changes <- svc.Status{State: svc.StopPending}
	app.StopAgent()
	changes <- svc.Status{State: svc.Stopped}
	return
}

// RunService runs the Agent as a Windows service
func RunService(isDebug bool) {
	writeLogFile("RunService")
	var err error
	if isDebug {
		elog = debug.New(config.ServiceName)
	} else {
		elog, err = eventlog.Open(config.ServiceName)
		if err != nil {
			return
		}
	}
	defer elog.Close()

	elog.Info(0x40000007, config.ServiceName)
	run := svc.Run

	err = run(config.ServiceName, &agentWindowsService{})
	if err != nil {
		elog.Error(0xc0000008, err.Error())
		return
	}
	elog.Info(0x40000004, config.ServiceName)
}

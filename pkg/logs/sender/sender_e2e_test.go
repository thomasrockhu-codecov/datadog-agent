// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package sender

import (
	"testing"

	"github.com/DataDog/datadog-agent/pkg/logs/client"
	"github.com/DataDog/datadog-agent/pkg/logs/client/http"
	"github.com/DataDog/datadog-agent/pkg/logs/message"
	"github.com/stretchr/testify/assert"
)

func TestSenderE2eSingleDestination(t *testing.T) {
	input := make(chan *message.Payload, 1)
	output := make(chan *message.Payload, 1)

	respondChan := make(chan struct{})

	server := http.NewTestServerWithOptions(200, 0, true, respondChan)

	destinations := client.NewDestinations([]client.Destination{server.Destination}, nil)

	sender := NewSender(input, output, destinations, 10)
	sender.Start()

	input <- &message.Payload{}
	input <- &message.Payload{}

	<-respondChan
	<-output

	<-respondChan
	<-output

	server.Stop()
	sender.Stop()
}

func TestSenderE2eDualReliableDestination(t *testing.T) {
	input := make(chan *message.Payload, 1)
	output := make(chan *message.Payload, 1)

	respondChan1 := make(chan struct{})
	server1 := http.NewTestServerWithOptions(200, 0, true, respondChan1)

	respondChan2 := make(chan struct{})
	server2 := http.NewTestServerWithOptions(200, 0, true, respondChan2)

	destinations := client.NewDestinations([]client.Destination{server1.Destination, server2.Destination}, nil)

	sender := NewSender(input, output, destinations, 10)
	sender.Start()

	input <- &message.Payload{}
	input <- &message.Payload{}

	<-respondChan1
	<-respondChan2
	<-output
	<-output

	<-respondChan1
	<-respondChan2
	<-output
	<-output

	server1.Stop()
	server2.Stop()
	sender.Stop()
}

func TestSenderE2eUnreliableAdditionalDestination(t *testing.T) {
	input := make(chan *message.Payload, 1)
	output := make(chan *message.Payload, 1)

	respondChan1 := make(chan struct{})
	server1 := http.NewTestServerWithOptions(200, 0, true, respondChan1)

	respondChan2 := make(chan struct{})
	server2 := http.NewTestServerWithOptions(200, 0, false, respondChan2)

	destinations := client.NewDestinations([]client.Destination{server1.Destination}, []client.Destination{server2.Destination})

	sender := NewSender(input, output, destinations, 10)
	sender.Start()

	input <- &message.Payload{}
	input <- &message.Payload{}

	<-respondChan1
	<-respondChan2
	<-output

	<-respondChan1
	<-respondChan2
	<-output

	server1.Stop()
	server2.Stop()
	sender.Stop()
}

func TestSenderE2eUnreliableStoppsWhenMainFails(t *testing.T) {
	input := make(chan *message.Payload, 1)
	output := make(chan *message.Payload, 1)

	reliableRespond := make(chan struct{})
	reliableServer := http.NewTestServerWithOptions(200, 0, true, reliableRespond)

	unreliableRespond := make(chan struct{})
	unreliableServer := http.NewTestServerWithOptions(200, 0, false, unreliableRespond)

	destinations := client.NewDestinations([]client.Destination{reliableServer.Destination}, []client.Destination{unreliableServer.Destination})

	sender := NewSender(input, output, destinations, 10)
	sender.Start()

	input <- &message.Payload{}

	<-reliableRespond
	<-unreliableRespond
	<-output

	reliableServer.ChangeStatus(500)

	input <- &message.Payload{}

	<-reliableRespond   // let it respond 500 once
	<-unreliableRespond // unreliable gets this log line because it hasn't fallen into a retry loop yet.
	<-reliableRespond   // its in a loop now, once we respond 500 a second time we know the sender has marked the endpoint as retrying

	// send another log
	input <- &message.Payload{}

	// reliable still stuck in retry loop - responding 500 over and over again.
	<-reliableRespond

	// unreliable should not be sending since all the reliable endpoints are failing.
	select {
	case <-unreliableRespond:
		assert.Fail(t, "unreliable sender should be waiting for main sender")
	default:
	}

	reliableServer.Stop()
	unreliableServer.Stop()
	sender.Stop()
}

func TestSenderE2eReliableContinuseWhenOneFails(t *testing.T) {
	input := make(chan *message.Payload, 1)
	output := make(chan *message.Payload, 1)

	reliableRespond1 := make(chan struct{})
	reliableServer1 := http.NewTestServerWithOptions(200, 0, true, reliableRespond1)

	reliableRespond2 := make(chan struct{})
	reliableServer2 := http.NewTestServerWithOptions(200, 0, false, reliableRespond2)

	destinations := client.NewDestinations([]client.Destination{reliableServer1.Destination, reliableServer2.Destination}, nil)

	sender := NewSender(input, output, destinations, 10)
	sender.Start()

	input <- &message.Payload{}

	<-reliableRespond1
	<-reliableRespond2
	<-output
	<-output

	reliableServer1.ChangeStatus(500)

	input <- &message.Payload{}

	<-reliableRespond1 // let it respond 500 once
	<-reliableRespond2 // Second endpoint gets the log line
	<-output
	<-reliableRespond1 // its in a loop now, once we respond 500 a second time we know the sender has marked the endpoint as retrying

	// send another log
	input <- &message.Payload{}

	// reliable still stuck in retry loop - responding 500 over and over again.
	<-reliableRespond1
	<-reliableRespond2 // Second output gets the line again
	<-output

	reliableServer1.Stop()
	reliableServer2.Stop()
	sender.Stop()
}

func TestSenderE2eReliableWhenOneFailsAndRecovers(t *testing.T) {
	input := make(chan *message.Payload, 1)
	output := make(chan *message.Payload, 1)

	reliableRespond1 := make(chan struct{})
	reliableServer1 := http.NewTestServerWithOptions(200, 0, true, reliableRespond1)

	reliableRespond2 := make(chan struct{})
	reliableServer2 := http.NewTestServerWithOptions(200, 0, false, reliableRespond2)

	destinations := client.NewDestinations([]client.Destination{reliableServer1.Destination, reliableServer2.Destination}, nil)

	sender := NewSender(input, output, destinations, 10)
	sender.Start()

	input <- &message.Payload{}

	<-reliableRespond1
	<-reliableRespond2
	<-output
	<-output

	reliableServer1.ChangeStatus(500)

	input <- &message.Payload{}

	<-reliableRespond1 // let it respond 500 once
	<-reliableRespond2 // Second endpoint gets the log line
	<-output
	<-reliableRespond1 // its in a loop now, once we respond 500 a second time we know the sender has marked the endpoint as retrying

	// send another log
	input <- &message.Payload{}

	// reliable still stuck in retry loop - responding 500 over and over again.
	<-reliableRespond1
	<-reliableRespond2 // Second output gets the line again
	<-output

	// Recover the first server
	reliableServer1.ChangeStatus(200)
	<-reliableRespond1 // respond 200

	<-output // get the buffered log line that was stuck

	// Make sure everything is unblocked
	input <- &message.Payload{}

	<-reliableRespond1
	<-reliableRespond2
	<-output
	<-output

	reliableServer1.Stop()
	reliableServer2.Stop()
	sender.Stop()
}
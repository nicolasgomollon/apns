# apns

[![GoDoc](https://godoc.org/github.com/timehop/apns?status.svg)](https://godoc.org/github.com/timehop/apns)
[![Build Status](https://travis-ci.org/timehop/apns.svg?branch=master)](https://travis-ci.org/timehop/apns)

A Go package to interface with the Apple Push Notification Service

## Features

This library implements a few features that we couldn't find in any one library elsewhere:

* **Long Lived Clients** - Apple's documentation say that you should hold [a persistent connection open](https://developer.apple.com/library/ios/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/Chapters/CommunicatingWIthAPS.html#//apple_ref/doc/uid/TP40008194-CH101-SW6) and not create new connections for every payload
* **Use of New Protocol** - Apple came out with v2 of their API with support for variable length payloads. This library uses that protocol.
* **Robust Send Guarantees** - APNS has asynchronous feedback on whether a push sent. That means that if you send pushes after a bad send, those pushes will be lost forever. Our library records the last N pushes, detects errors, and is able to resend the pushes that could have been lost. [More reading](http://redth.codes/the-problem-with-apples-push-notification-ser/)

## API Compatibility

The apns package may undergo breaking changes. A tool like [godep](https://github.com/tools/godep) is recommended to vendor the current release.

## Install

```
go get github.com/timehop/apns
```

Checkout the `develop` branch for the current work in progress.

## Usage

### Sending a push notification (basic)

```go
client, _ := apns.NewClient(apns.ProductionGateway, apnsCert, apnsKey)

payload := apns.NewPayload()
payload.APS.Alert.Body = "I am a push notification!"
payload.APS.Badge.Set(5)
payload.APS.Sound = "turn_down_for_what.aiff"

notif := apns.NewNotification()
notif.Payload = payload
notif.DeviceToken = "A_DEVICE_TOKEN"
notif.Priority = apns.PriorityImmediate

client.Send(notif)

// Wait for all notifications to be pushed before exiting.
for (client.Sent + client.Failed) < client.Len {
	time.Sleep(30 * time.Second)
}
```

### Sending a push notification with error handling

```go
client, err := apns.NewClient(apns.ProductionGateway, apnsCert, apnsKey, true /* optional verbose mode */)
if err != nil {
	log.Fatal("could not create new client", err.Error()
}

go func() {
	for f := range client.FailedNotifs {
		fmt.Println("Notif", f.Notif.ID, "failed with", f.Err.Error())
	}
}()

payload := apns.NewPayload()
payload.APS.Alert.Body = "I am a push notification!"
payload.APS.Badge.Set(5)
payload.APS.Sound = "turn_down_for_what.aiff"
payload.APS.ContentAvailable = 1

payload.SetCustomValue("link", "zombo://dot/com")
payload.SetCustomValue("game", map[string]int{"score": 234})

notif := apns.NewNotification()
notif.Payload = payload
notif.DeviceToken = "A_DEVICE_TOKEN"
notif.Priority = apns.PriorityImmediate
notif.Identifier = 12312, // Integer for APNS
notif.ID = "user_id:timestamp", // ID not sent to Apple – to identify error notifications

client.Send(notif)

// Wait for all notifications to be pushed before exiting.
for (client.Sent + client.Failed) < client.Len {
	time.Sleep(30 * time.Second)
}
```

### Retrieving feedback

```go
f, err := apns.NewFeedback(s.Address(), DummyCert, DummyKey)
if err != nil {
	log.Fatal("Could not create feedback", err.Error())
}

for ft := range f.Receive() {
	fmt.Println("Feedback for token:", ft.DeviceToken)
}
```

Note that the channel returned from `Receive` will close after the
[feedback service](https://developer.apple.com/library/ios/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/Chapters/CommunicatingWIthAPS.html#//apple_ref/doc/uid/TP40008194-CH101-SW3)
has no more data to send.

## Running the tests

We use [Ginkgo](https://onsi.github.io/ginkgo) for our testing framework and
[Gomega](http://onsi.github.io/gomega/) for our matchers. To run the tests:

```
go get github.com/onsi/ginkgo/ginkgo
go get github.com/onsi/gomega
ginkgo -randomizeAllSpecs
```

## Contributing

- Fork the repo ([Recommended process](https://splice.com/blog/contributing-open-source-git-repositories-go/))
- Make your changes
- [Run the tests](https://github.com/timehop/apns#running-the-tests)
- Submit a pull request

## License

[MIT License](https://github.com/timehop/apns/blob/master/LICENSE)

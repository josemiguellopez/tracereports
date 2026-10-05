module github.com/josemiguellopez/tracereports/examples/playwright-go

go 1.22

require (
	github.com/josemiguellopez/tracereports/client/go v0.0.0
	github.com/mxschmitt/playwright-go v0.6201.1
)

require (
	github.com/deckarep/golang-set/v2 v2.8.0 // indirect
	github.com/go-stack/stack v1.8.1 // indirect
)

// El ejemplo usa el cliente de este mismo checkout. En tu proyecto no hace falta el replace:
// go get github.com/josemiguellopez/tracereports/client/go@latest
replace github.com/josemiguellopez/tracereports/client/go => ../../client/go

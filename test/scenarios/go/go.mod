module github.com/josemiguellopez/tracereports/test/scenarios/go

go 1.22

require github.com/josemiguellopez/tracereports/client/go v0.0.0

// el cliente se compila desde el repositorio
replace github.com/josemiguellopez/tracereports/client/go => ../../../client/go

# TraceReports: un único binario estático (Go sin CGO) sobre una imagen mínima sin shell.
FROM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY web.go ./
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
# la versión del tag (la pone release.yml); vacía: "dev"
ARG VERSION=
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/tracereports ./cmd \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/tracereports /tracereports
# /data pertenece al usuario sin privilegios: un volumen nuevo hereda estos permisos
COPY --from=build --chown=65532:65532 /out/data /data
ENV PORT=8080 DATA_DIR=/data TRACEREPORTS_RUNTIME=docker
VOLUME /data
EXPOSE 8080
USER nonroot:nonroot
# sin shell ni curl en la imagen: el propio binario pregunta a /healthz
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["/tracereports", "health"]
ENTRYPOINT ["/tracereports"]

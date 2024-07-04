// Code generated by Wire. DO NOT EDIT.

//go:generate go run -mod=mod github.com/google/wire/cmd/wire
//go:build !wireinject
// +build !wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"proxy-service/internal/conf"
	"proxy-service/internal/server"
	"proxy-service/internal/service"
)

import (
	_ "go.uber.org/automaxprocs"
)

// Injectors from wire.go:

// wireApp init kratos application.
func wireApp(confServer *conf.Server, data *conf.Data, logger log.Logger) (*kratos.App, func(), error) {
	openAIService := service.NewOpenAIService()
	grpcServer := server.NewGRPCServer(confServer, openAIService, logger)
	httpServer := server.NewHTTPServer(confServer, openAIService, logger)
	app := newApp(logger, grpcServer, httpServer)
	return app, func() {
	}, nil
}

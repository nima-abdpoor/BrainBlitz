package main

import (
	"BrainBlitz.com/game/internal/controller"
	"BrainBlitz.com/game/internal/core/server/http"
	"BrainBlitz.com/game/internal/core/service"
	"BrainBlitz.com/game/internal/infra/config"
	"BrainBlitz.com/game/internal/infra/repository"
	"github.com/gin-gonic/gin"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Create a new instance of the Gin router
	ginInstance := gin.New()
	ginInstance.Use(gin.Recovery())

	db, err := repository.NewDB(config.DatabaseConfig{
		Driver:                 "mysql",
		Url:                    "bbGame:root@tcp(127.0.0.1:3310)/brainBlitz_db?charset=utf8mb4&parseTime=true&loc=UTC&tls=false&readTimeout=3s&writeTimeout=3s&timeout=3s&clientFoundRows=true",
		ConnMaxLifeTimeMinutes: 3,
		MaxOpenCons:            10,
		MaxIdleCons:            1,
	})
	if err != nil {
		log.Fatalf("failed to new database err=%s\n", err.Error())
	}

	//create the UserRepository
	userRepo := repository.NewUserRepository(db)

	//create the user service
	userService := service.NewUserService(userRepo)

	userController := controller.NewUserController(ginInstance, userService)
	userController.InitRouter()

	//create httpServer
	httpServer := http.NewHTTPServer(ginInstance, config.HttpServerConfig{
		Port: 8000,
	})

	httpServer.Start()
	defer httpServer.Stop()

	// Listen for OS signals to perform a graceful shutdown
	log.Printf("listening signals on %d ...", os.Getpid())
	c := make(chan os.Signal, 1)
	signal.Notify(
		c,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGTERM,
	)
	<-c
	log.Println("graceful shutdown...")
}

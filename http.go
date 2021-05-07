package main

import (
	"fmt"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"github.com/sirupsen/logrus"
	"net/http"
	"os"
	"strings"
	"time"
)

func NewRouter(coinFlip *CoinFlip) *Router {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Debug = false
	e.Server.ReadTimeout = 5 * time.Second
	e.Server.WriteTimeout = 10 * time.Second
	e.Server.IdleTimeout = 120 * time.Second
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())
	e.Use(middleware.GzipWithConfig(middleware.GzipConfig{Level: 5}))

	router := new(Router)
	router.echo = e
	router.config = coinFlip.config.Http
	router.database = coinFlip.database
	router.coinFlip = coinFlip

	e.GET("/", func(context echo.Context) error {
		return context.Redirect(http.StatusTemporaryRedirect, "/status")
	})
	e.GET("/status", router.Status)
	e.GET("/result/:txhash", router.Result)

	return router
}

type Router struct {
	echo     *echo.Echo
	config   ConfigHttp
	coinFlip *CoinFlip
	database *Database
}

func (r *Router) Status(c echo.Context) error {
	res := Map{
		"uptime":                   time.Now().Sub(r.coinFlip.started).String(),
		"balance":                  fmt.Sprintf("%.8f", r.coinFlip.state.Balance),
		"topupAddress":             r.coinFlip.state.TopupAddress,
		"headsAddress":             r.coinFlip.state.HeadsAddress,
		"tailsAddress":             r.coinFlip.state.TailsAddress,
		"minimumThreshold":         fmt.Sprintf("%.8f", r.coinFlip.minimumThreshold),
		"maximumThreshold":         fmt.Sprintf("%.8f", r.coinFlip.maximumThreshold),
		"won":                      r.coinFlip.state.WonCount,
		"lost":                     r.coinFlip.state.LostCount,
		"refunded":                 r.coinFlip.state.RefundCount,
		"dashBuild":                r.coinFlip.dash.buildVersion,
		"dashBlockCount":           r.coinFlip.dash.blockCount,
		"dashProtocol":             r.coinFlip.dash.protocolVersion,
		"dashVerificationProgress": r.coinFlip.dash.verificationProgress,
	}

	version := os.Getenv("VERSION")
	if version != "" {
		res["version"] = version
	}

	return c.JSON(http.StatusOK, res)
}

func (r *Router) Result(c echo.Context) error {
	txhash := strings.TrimSpace(c.Param("txhash"))
	if txhash == "" {
		return echo.ErrNotFound
	}

	result, err := r.database.GetResult(txhash)
	if err != nil {
		return echo.ErrNotFound
	}

	res := Map{
		"received":             result.Received,
		"transactionHash":      result.TransactionHash,
		"transactionFrom":      result.TransactionFrom,
		"transactionSignature": result.TransactionSignature,
		"winner":               result.Winner,
	}

	if result.HeadsAmount > 0 {
		res["headsAmount"] = result.HeadsAmount
	}

	if result.TailsAmount > 0 {
		res["tailsAmount"] = result.TailsAmount
	}

	if result.RefundReason != "" {
		res["refundReason"] = result.RefundReason
	}

	if result.RefundHash != "" {
		res["refundHash"] = result.RefundHash
	}

	if result.PayoutAmount > 0 {
		res["payoutAmount"] = result.PayoutAmount
	}

	if result.PayoutHash != "" {
		res["payoutHash"] = result.PayoutHash
	}

	if result.PayoutError != "" {
		res["payoutError"] = result.PayoutError
	}

	return c.JSON(http.StatusOK, res)
}

func (r *Router) Run() {
	port := r.config.Port
	if port == "" {
		port = "3000"
	}
	logrus.Infof("Http server running on port %s", port)
	_ = r.echo.Start(":" + port)
}

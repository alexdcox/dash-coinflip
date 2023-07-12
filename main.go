package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func main() {
	config := new(Config)
	err := config.Load()
	panicIfErr(err)

	if config.LogLevel != "" {
		if level, err := logrus.ParseLevel(config.LogLevel); err == nil {
			logrus.SetLevel(level)
		} else {
			logrus.Warnf("Unable to parse configured log level '%s'", config.LogLevel)
		}
	}

	database, err := NewDatabase(config.DatabasePath)
	panicIfErr(err)

	err = database.Setup()
	panicIfErr(err)

	coinFlip, err := NewCoinFlip(config, database)
	panicIfErr(err)

	err = coinFlip.Run()
	panicIfErr(err)

	if config.Http.Enabled {
		router := NewRouter(coinFlip)
		go router.Run()
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, os.Kill)
	<-c

	err = coinFlip.Shutdown()
	panicIfErr(err)
}

const (
	DefaultMinimumThreshold = 0.001
	DefaultMaximumThreshold = 1.0
)

const (
	RefundReasonNotInstantSend       = "NOT_INSTANT_SEND"
	RefundReasonAmountBelowThreshold = "AMOUNT_BELOW_THRESHOLD"
	RefundReasonAmountAboveThreshold = "AMOUNT_ABOVE_THRESHOLD"
	RefundReasonInsufficientFunds    = "INSUFFICIENT_FUNDS"
	RefundReasonAmbiguousChoice      = "AMBIGUOUS_CHOICE"
)

type CoinFlipState struct {
	Balance      float64
	HeadsAddress string
	TailsAddress string
	TopupAddress string
	WonCount     int64
	LostCount    int64
	RefundCount  int64
}

type Topup struct {
	Hash     string
	From     string
	Amount   float64
	Received time.Time
}

type FlipResult struct {
	TransactionHash      string
	TransactionFrom      string
	Received             time.Time
	RefundReason         string
	RefundHash           string
	RefundError          string
	HeadsAmount          float64
	TailsAmount          float64
	Winner               bool
	PayoutAmount         float64
	PayoutHash           string
	PayoutError          string
	TransactionSignature string
}

func NewCoinFlip(config *Config, database *Database) (coinFlip *CoinFlip, err error) {
	coinFlip = new(CoinFlip)
	coinFlip.config = config
	coinFlip.database = database
	coinFlip.started = time.Now()

	coinFlip.state, err = database.GetState()
	if err != nil {
		return
	}

	if config.MinimumThreshold == 0 {
		logrus.Warnf("Minimum threshold not set, using %f", DefaultMinimumThreshold)
		config.MinimumThreshold = DefaultMinimumThreshold
	}

	if config.MaximumThreshold == 0 {
		logrus.Warnf("Maximum threshold not set, using %f", DefaultMaximumThreshold)
		config.MinimumThreshold = DefaultMaximumThreshold
	}

	coinFlip.minimumThreshold = config.MinimumThreshold
	coinFlip.maximumThreshold = config.MaximumThreshold

	logrus.Infof(
		"Receive threshold is from %f to %f DASH",
		coinFlip.minimumThreshold,
		coinFlip.maximumThreshold)

	coinFlip.dash, err = NewDashNode(coinFlip.config.Dash)

	return
}

type CoinFlip struct {
	config           *Config
	database         *Database
	dash             *DashNode
	minimumThreshold float64
	maximumThreshold float64
	state            *CoinFlipState
	started          time.Time
}

func (c *CoinFlip) Shutdown() (err error) {
	logrus.Info("Shutting down gracefully...")
	err = c.database.StoreState(c.state)
	if err != nil {
		return
	}
	err = c.database.client.Close()
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	logrus.Info("Shutdown complete")
	return nil
}

func (c *CoinFlip) Run() (err error) {
	for {
		loading, loadingErr := c.dash.IsLoading()
		if loadingErr != nil {
			err = loadingErr
			return
		}
		if !loading {
			break
		}
		logrus.Info("Waiting for node rpc server to start... 💤")
		time.Sleep(time.Second * 10)
	}

	if c.config.Dash.Wallet == "" {
		logrus.Info("Using wallet '' (i.e. the default)")
	} else {
		logrus.Infof("Using wallet '%s'", c.config.Dash.Wallet)
	}

	balanceCheck := func() error {
		walletBalance, err2 := c.dash.GetBalance()
		logrus.Infof("Verifying server balance - expected %.8f == actual %.8f", c.state.Balance, walletBalance)
		if err2 != nil {
			return err2
		}
		if fmt.Sprintf("%.8f", walletBalance) != fmt.Sprintf("%.8f", c.state.Balance) {
			return fmt.Errorf(
				"balance mismatch between wallet and state. expected %.8f, got %.8f from wallet '%v'",
				c.state.Balance,
				walletBalance,
				c.config.Dash.Wallet,
			)
		}
		return nil
	}

	nodeBalance, walletBalanceErr := c.dash.GetBalance()
	if walletBalanceErr != nil {
		if strings.Contains(walletBalanceErr.Error(), "Requested wallet does not exist or is not loaded") {
			logrus.Infof("Wallet not found, creating '%s'", c.config.Dash.Wallet)
			_, err = c.dash.CreateWallet()
			if err != nil {
				return err
			}
			c.state.TopupAddress = ""
			c.state.HeadsAddress = ""
			c.state.TailsAddress = ""
		}
		c.state.Balance = 0
	} else {
		if c.config.ReadInitialBalanceFromNode {
			logrus.Infof("Overwriting initial expected balance from current node balance: %.8f", nodeBalance)
			c.state.Balance = nodeBalance
		} else {
			err = balanceCheck()
			if err != nil {
				return
			}
		}
	}

	if c.state.TopupAddress == "" {
		logrus.Info("Generating new topup address")
		c.state.TopupAddress, err = c.dash.NewAddress()
		if err != nil {
			return
		}
	}

	if c.state.HeadsAddress == "" {
		logrus.Info("Generating new address for 'heads'")
		c.state.HeadsAddress, err = c.dash.NewAddress()
		if err != nil {
			return
		}
	}

	if c.state.TailsAddress == "" {
		logrus.Info("Generating new address for 'tails'")
		c.state.TailsAddress, err = c.dash.NewAddress()
		if err != nil {
			return
		}
	}

	logrus.Infof("Topup address is: %s", c.state.TopupAddress)
	logrus.Infof("Heads address is: %s", c.state.HeadsAddress)
	logrus.Infof("Tails address is: %s", c.state.TailsAddress)

	c.dash.WatchAddress(c.state.TopupAddress)
	c.dash.WatchAddress(c.state.HeadsAddress)
	c.dash.WatchAddress(c.state.TailsAddress)

	logBalance := func() {
		logrus.Infof("Server balance is %f 💰", c.state.Balance)
		if c.state.Balance < c.config.MaximumThreshold {
			logrus.Warnf("Server balance is below the maximum threshold, a topup is advised ❗️")
		}
	}

	transactionMutex := new(sync.Mutex)
	c.dash.OnTransaction = func(tx *Transaction) {
		transactionMutex.Lock()
		defer transactionMutex.Unlock()

		logrus.Debugf("Handling %s transaction (i.s: %t) - %s", tx.Type, tx.InstantSend, tx.Hash)

		if _, err = c.database.GetResult(tx.Hash); err == nil {
			logrus.Debugf("Skipping already-handled transaction event: %s", tx.Hash)
			return
		}

		if topupAmount, usesTopupAddress := tx.Amounts[c.state.TopupAddress]; usesTopupAddress {
			if _, err = c.database.GetTopup(tx.Hash); err == nil {
				logrus.Debugf("Skipping already-handled topup event: %s", tx.Hash)
				return
			}
			c.state.Balance += topupAmount
			topup := &Topup{
				Hash:     tx.Hash,
				From:     tx.RefundAddress,
				Amount:   topupAmount,
				Received: time.Now(),
			}
			err := c.database.StoreTopup(topup)
			if err != nil {
				logrus.WithError(err).Panicf(
					"Unable to persist topup of %.8f received with tx %s, cannot de-duplicate transactions",
					topupAmount,
					tx.Hash,
				)
			}
			logrus.Infof("Topup of %.8f received with tx %s", topupAmount, tx.Hash)
			logBalance()
			return
		}

		headsAmount, usesHeadsAddress := tx.Amounts[c.state.HeadsAddress]
		tailsAmount, usesTailsAddress := tx.Amounts[c.state.TailsAddress]

		if !usesHeadsAddress && !usesTailsAddress {
			return
		}

		result := &FlipResult{
			TransactionHash:      tx.Hash,
			TransactionFrom:      tx.RefundAddress,
			TransactionSignature: tx.BlsSignature,
			Received:             time.Now(),
			HeadsAmount:          headsAmount,
			TailsAmount:          tailsAmount,
		}
		defer func() {
			err := c.database.StoreResult(result)
			if err != nil {
				logrus.WithError(err).Errorf("Failed to persist result to database for tx %s", tx.Hash)
			}
		}()

		if !(usesHeadsAddress != usesTailsAddress) {
			var refundTransactions []string
			result.RefundReason = RefundReasonAmbiguousChoice
			if headsAmount > 0 {
				txid, err := c.dash.Send(tx.RefundAddress, headsAmount)
				if err != nil {
					logrus.WithError(err).Errorf("Failed to send refund of %f to %s", headsAmount, tx.RefundAddress)
				} else {
					refundTransactions = append(refundTransactions, txid)
				}
			}
			if tailsAmount > 0 {
				txid, err := c.dash.Send(tx.RefundAddress, tailsAmount)
				if err != nil {
					logrus.WithError(err).Errorf("Failed to send refund of %f to %s", tailsAmount, tx.RefundAddress)
				} else {
					refundTransactions = append(refundTransactions, txid)
				}
			}
			result.RefundHash = strings.Join(refundTransactions, ",")
			c.state.RefundCount += 1
			return
		}

		amount := headsAmount
		isEvenSelection := true
		if usesTailsAddress {
			amount = tailsAmount
			isEvenSelection = false
		}

		c.state.Balance += amount

		payoutAmount := amount * (1.99)

		refund := func() {
			txid, err := c.dash.Send(tx.RefundAddress, amount)
			if err != nil {
				logrus.WithError(err).Errorf("Failed to send refund of %f to %s", amount, tx.RefundAddress)
				result.RefundError = err.Error()
			} else {
				logrus.Infof("Sent refund of %f to %s for reason %s %s", amount, tx.RefundAddress, result.RefundReason, txid)
				result.RefundHash = txid
			}
			c.state.RefundCount += 1
			c.state.Balance -= amount
		}

		switch {
		case !tx.InstantSend:
			result.RefundReason = RefundReasonNotInstantSend
			refund()

		case amount < c.minimumThreshold:
			result.RefundReason = RefundReasonAmountBelowThreshold
			refund()

		case amount > c.maximumThreshold:
			result.RefundReason = RefundReasonAmountAboveThreshold
			refund()

		case payoutAmount > c.state.Balance:
			result.RefundReason = RefundReasonInsufficientFunds
			refund()

		case isEvenSelection != tx.HasEvenBlsSignature():
			logrus.Infof("No luck this time 🐠 tx %s of %f", tx.Hash, amount)
			c.state.LostCount += 1
			logBalance()

		default:
			result.Winner = true
			result.PayoutAmount = payoutAmount
			txid, err := c.dash.Send(tx.RefundAddress, payoutAmount)
			if err != nil {
				logrus.WithError(err).Errorf("Failed to payout %f to %s", payoutAmount, tx.RefundAddress)
				result.PayoutError = err.Error()
				return
			}
			logrus.Infof("We have a winner! 🥳🎉 Paid out %f to %s with tx %s", payoutAmount, tx.RefundAddress, txid)
			result.PayoutHash = txid
			c.state.Balance -= payoutAmount
			c.state.WonCount += 1
			logBalance()
		}

		// Do a balance check here but don't worry about the actual result.
		// Just provide helpful logs.
		// It's possible for the balance to be greater than expected if the node
		// receives many transactions in quick succession before we've
		// processed the zmq message.
		_ = balanceCheck()
	}

	err = c.dash.Connect()
	panicIfErr(err)

	logrus.Info("Coin flip game running")
	logBalance()

	return
}

type Map map[string]interface{}
type MapArray []interface{}

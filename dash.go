package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dashpay/dashd-go/chaincfg"
	"github.com/dashpay/dashd-go/txscript"
	"github.com/dashpay/dashd-go/wire"
	"github.com/labstack/echo"
	zmq "github.com/pebbe/zmq4"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

var (
	MainNetParams = chaincfg.MainNetParams
	TestNetParams = chaincfg.TestNet3Params
)

func init() {
	MainNetParams.PubKeyHashAddrID = 0x4c // 76
	MainNetParams.ScriptHashAddrID = 0x10 // 16
	MainNetParams.PrivateKeyID = 0xcc     // 204

	TestNetParams.Name = "testnet"
	TestNetParams.Net = 0xffcae2ce
	TestNetParams.PubKeyHashAddrID = byte(140)
	TestNetParams.ScriptHashAddrID = byte(19)
	TestNetParams.PrivateKeyID = byte(239)

}

// currently unused messages:
// MessageTypeRawBlock     = "rawblock"
// MessageTypeRawTx        = "rawtx"

const (
	MessageTypeRawTxLockSig = "rawtxlocksig"
)

type Transaction struct {
	Type          string
	Hash          string
	InstantSend   bool
	BlsSignature  string
	RefundAddress string
	Amounts       map[string]float64
}

func (t *Transaction) HasEvenBlsSignature() bool {
	blsInt, _ := new(big.Int).SetString(t.BlsSignature, 16)
	blsEvenNumber := new(big.Int).Mod(blsInt, big.NewInt(2)).Int64() == 0
	return blsEvenNumber
}

func NewDashNode(config ConfigDashNode) (dashNode *DashNode, err error) {
	dashNode = new(DashNode)
	dashNode.config = config

	if strings.Contains(dashNode.config.Network, "main") {
		logrus.Info("Using node 'main' network")
		dashNode.params = &MainNetParams
	} else {
		logrus.Info("Using node 'test' network")
		dashNode.params = &TestNetParams
	}

	dashNode.httpclient = new(http.Client)
	dashNode.httpclient.Timeout = time.Second * 10

	zmqContext, err := zmq.NewContext()
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	dashNode.zmqContext = zmqContext

	return
}

type DashNode struct {
	OnTransaction        func(tx *Transaction)
	watchedAddresses     []string
	config               ConfigDashNode
	httpclient           *http.Client
	timeout              time.Duration
	zmqContext           *zmq.Context
	params               *chaincfg.Params
	buildVersion         string
	protocolVersion      string
	blockCount           int64
	verificationProgress float64
}

func (d *DashNode) zmqSubscribe(messageType string) (subscriber *zmq.Socket, err error) {
	subscriber, err = d.zmqContext.NewSocket(zmq.SUB)
	if err != nil {
		err = errors.WithStack(err)
		return
	}

	err = subscriber.Connect(d.config.ZmqEndpoint)
	if err != nil {
		err = errors.WithStack(err)
		return
	}

	err = errors.WithStack(subscriber.SetSubscribe(messageType))

	return
}

func (d *DashNode) Connect() (err error) {
	networkInfo, err := d.GetNetworkInfo()
	if err != nil {
		return
	}

	d.buildVersion = networkInfo.Get("buildversion").String()
	d.protocolVersion = networkInfo.Get("protocolversion").String()

	go func() {
		var blockCountCheckDelay time.Duration

		for {
			blockchainInfo, err := d.GetBlockchainInfo()
			if err != nil {
				return
			}

			d.blockCount = blockchainInfo.Get("blocks").Int()
			d.verificationProgress = blockchainInfo.Get("verificationprogress").Float()

			if d.verificationProgress > 0.999 {
				blockCountCheckDelay = time.Second * 10
			} else {
				blockCountCheckDelay = time.Second * 3
			}

			time.Sleep(blockCountCheckDelay)
		}
	}()

	isWatchedTransaction := func(tx *wire.MsgTx) bool {
		for _, txout := range tx.TxOut {
			_, addresses, _, _ := txscript.ExtractPkScriptAddrs(txout.PkScript, d.params)
			if len(addresses) != 1 {
				continue
			}
			toCryptoAddress := addresses[0].String()
			if toCryptoAddress == "" {
				continue
			}
			for _, watchedAddress := range d.watchedAddresses {
				if watchedAddress == toCryptoAddress {
					return true
				}
			}
		}
		return false
	}

	type WireTransaction struct {
		Type     string
		Data     []byte
		Received time.Time
	}
	type ZmqMessageCallback func(wireTx *WireTransaction)

	handleWireTransaction := func(wireTx *WireTransaction) {
		reader := bytes.NewBuffer(wireTx.Data)
		tx := new(wire.MsgTx)
		err = tx.Deserialize(reader)
		if err != nil {
			reader.Reset()
			err = tx.DeserializeNoWitness(reader)
			if err != nil {
				return
			}
		}

		hash := tx.TxHash().String()

		if !isWatchedTransaction(tx) {
			logrus.Debugf("Ignoring transaction to non-watched addresses: %s", hash)
			return
		}

		var jsn gjson.Result
		jsn, err = d.GetRawTransaction(hash)
		if err != nil {
			return
		}

		logrus.Debugf("%s - %s\n", wireTx.Type, tx.TxHash().String())

		if len(jsn.Get("vin").Array()) < 1 || len(jsn.Get("vout").Array()) < 1 {
			logrus.Debugf("Ignoring non-standard transaction with hash: %s", hash)
			return
		}

		refundAddress := jsn.Get("vin.0.address").String()
		if refundAddress == "" {
			logrus.Debugf("Ignoring transaction without refund address: %s", hash)
			return
		}

		amounts := make(map[string]float64)
		jsn.Get("vout").ForEach(func(key, value gjson.Result) bool {
			amount := value.Get("value").Float()
			addresses := value.Get("scriptPubKey.addresses").Array()
			if len(addresses) > 1 {
				return true
			}
			address := addresses[0].String()
			if _, hasKey := amounts[address]; !hasKey {
				amounts[address] = 0.0
			}
			amounts[address] += amount
			return true
		})

		transaction := &Transaction{
			Type:          wireTx.Type,
			Hash:          hash,
			Amounts:       amounts,
			RefundAddress: refundAddress,
		}

		if wireTx.Type == MessageTypeRawTxLockSig {
			if jsn.Get("instantlock").Bool() {
				messageLen := len(wireTx.Data)
				blsSignatureLen := 96
				blsSignature := wireTx.Data[messageLen-blsSignatureLen : messageLen]
				transaction.InstantSend = true
				transaction.BlsSignature = fmt.Sprintf("%x", blsSignature)
			}
		}

		d.OnTransaction(transaction)
	}

	delayCallback := func(callback ZmqMessageCallback, delay time.Duration) ZmqMessageCallback {
		var delayedBuffer []*WireTransaction
		delayedBufferMutex := new(sync.Mutex)
		go func() {
			for {
				time.Sleep(delay)
				var leaveBuffer []*WireTransaction
				var takeBuffer []*WireTransaction
				delayedBufferMutex.Lock()
				for _, delayedTx := range delayedBuffer {
					if delayedTx.Received.Before(time.Now().Add(-delay)) {
						takeBuffer = append(takeBuffer, delayedTx)
					} else {
						leaveBuffer = append(leaveBuffer, delayedTx)
					}
				}
				delayedBuffer = leaveBuffer
				delayedBufferMutex.Unlock()
				for _, delayedTx := range takeBuffer {
					callback(delayedTx)
				}
			}
		}()
		return func(tx *WireTransaction) {
			delayedBufferMutex.Lock()
			delayedBuffer = append(delayedBuffer, tx)
			delayedBufferMutex.Unlock()
		}
	}

	subscribeToMessages := func(messageType string, callback ZmqMessageCallback) {
		go func() {
			logrus.Debug("@zmq subscribe", messageType)
			subscriber, err := d.zmqSubscribe(messageType)
			if err != nil {
				return
			}

			for {
				var message [][]byte
				message, err = subscriber.RecvMessageBytes(0)
				if err != nil {
					return
				}
				logrus.Debug("@zmq message", messageType)
				if len(message) != 3 || string(message[0]) != messageType {
					logrus.Debug("dropped zmq message")
					continue
				}

				messageContent := message[1]
				callback(&WireTransaction{
					Data:     messageContent,
					Type:     messageType,
					Received: time.Now(),
				})
			}
		}()
	}

	// NOTE: The delay is there to give the transaction more time to appear as
	//       an instant send transaction to the node, and be reflected in the
	//       rpc response.
	subscribeToMessages(MessageTypeRawTxLockSig, delayCallback(handleWireTransaction, time.Second*2))

	return
}

func (d *DashNode) CreateWallet() (address string, err error) {
	jsn, _, err := d.req(Map{
		"method": "createwallet",
		"params": MapArray{d.config.Wallet},
	})
	if err != nil {
		return
	}
	address = jsn.Get("result").String()
	return
}

func (d *DashNode) GetBalance() (balance float64, err error) {
	jsn, _, err := d.req(Map{
		"method": "getbalance",
		"wallet": d.config.Wallet,
	})
	if err != nil {
		return
	}
	balance = jsn.Get("result").Float()
	return
}

func (d *DashNode) NewAddress() (address string, err error) {
	jsn, _, err := d.req(Map{
		"method": "getnewaddress",
		"wallet": d.config.Wallet,
	})
	if err != nil {
		return
	}
	address = jsn.Get("result").String()
	return
}

func (d *DashNode) WatchAddress(address string) {
	d.watchedAddresses = append(d.watchedAddresses, address)
	return
}

func (d *DashNode) GetRawTransaction(hash string) (jsn gjson.Result, err error) {
	jsn, _, err = d.req(Map{
		"method": "getrawtransaction",
		"params": MapArray{hash, true},
	})
	if err != nil {
		return
	}
	jsn = jsn.Get("result")
	return
}

func (d *DashNode) GetNetworkInfo() (jsn gjson.Result, err error) {
	jsn, _, err = d.req(Map{"method": "getnetworkinfo"})
	if err != nil {
		return
	}
	jsn = jsn.Get("result")
	return
}

func (d *DashNode) GetBlockchainInfo() (jsn gjson.Result, err error) {
	jsn, _, err = d.req(Map{"method": "getblockchaininfo"})
	if err != nil {
		return
	}
	jsn = jsn.Get("result")
	return
}

func (d *DashNode) GetBlockCount() (count int64, err error) {
	jsn, _, err := d.req(Map{"method": "getblockcount"})
	if err != nil {
		return
	}
	count = jsn.Get("result").Int()
	return
}

func (d *DashNode) IsLoading() (loading bool, err error) {
	jsn, _, err := d.req(Map{"method": "getblockcount"})
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			loading = true
			err = nil
			return
		}
	}
	if jsn.Get("error.code").Exists() {
		loading = true
		err = nil
	}
	return
}

func (d *DashNode) ListUnspent() (jsn gjson.Result, err error) {
	jsn, _, err = d.req(Map{
		"method": "listunspent",
		"wallet": d.config.Wallet,
		"params": MapArray{0},
	})
	if err != nil {
		return
	}
	jsn = jsn.Get("result")
	return
}

func (d *DashNode) Send(address string, amount float64) (txid string, err error) {
	networkInfo, err := d.GetNetworkInfo()
	if err != nil {
		return
	}

	relayFee := networkInfo.Get("relayfee").Float()

	listUnspent, err := d.ListUnspent()
	if err != nil {
		return
	}
	runningTotal := 0.0
	var unspentIn []Map
	listUnspent.ForEach(func(key, value gjson.Result) bool {
		id := value.Get("txid").String()
		if !value.Get("spendable").Bool() {
			logrus.Debugf("skipping unspent txid %s, not spendable\n", id)
			return true
		}
		if !value.Get("safe").Bool() {
			logrus.Debugf("skipping unsafe txid %s, not safe to spend\n", id)
			return true
		}
		uamount := value.Get("amount").Float()
		vout := value.Get("vout").Int()
		unspentIn = append(unspentIn, Map{
			"txid": id,
			"vout": vout,
		})
		runningTotal += uamount
		if runningTotal >= amount+relayFee {
			return false
		}
		return true
	})

	if len(unspentIn) < 1 {
		err = errors.New("no unspent transactions available, exiting")
		return
	}

	changeOut := runningTotal - amount
	changeAddress, err := d.NewAddress()
	if err != nil {
		return
	}

	outs := []Map{
		{address: fmt.Sprintf("%.8f", amount-relayFee)},
		{changeAddress: fmt.Sprintf("%.8f", changeOut)},
	}

	createRawParams := MapArray{
		unspentIn,
		outs,
	}

	rawTransaction, _, err := d.req(Map{
		"method": "createrawtransaction",
		"wallet": d.config.Wallet,
		"params": createRawParams,
	})
	if err != nil {
		if j, err := json.Marshal(createRawParams); err == nil {
			logrus.Errorf("Create raw transaction failed with params: %s", j)
		}
		return
	}
	rawTransaction = rawTransaction.Get("result")

	signedRawTransaction, _, err := d.req(Map{
		"method": "signrawtransactionwithwallet",
		"wallet": d.config.Wallet,
		"params": MapArray{rawTransaction.String()},
	})
	if err != nil {
		err = errors.Errorf("failed to sign raw transaction: %s, err: %+v", rawTransaction, err)
		return
	}
	signedRawTransaction = signedRawTransaction.Get("result")
	signedRawTransactionHex := signedRawTransaction.Get("hex").String()

	if !signedRawTransaction.Get("complete").Bool() {
		err = errors.Errorf("failed to sign raw transaction: %s", signedRawTransactionHex)
		return
	}

	sendResult, _, err := d.req(Map{
		"method": "sendrawtransaction",
		"wallet": d.config.Wallet,
		"params": MapArray{signedRawTransactionHex, 0},
	})
	if err != nil {
		err = errors.Errorf("failed to send signed raw transaction: %s, err: %+v", signedRawTransactionHex, err)
		return
	}
	sendResult = sendResult.Get("result")

	txid = sendResult.String()
	if txid == "" {
		logrus.Debug("DASH ERROR", sendResult.Raw)
		err = errors.New("failed to determne txid")
		return
	}

	return
}

func (d *DashNode) req(m Map) (jsn gjson.Result, rsp *http.Response, err error) {
	jsonin, err := json.Marshal(m)
	if err != nil {
		err = errors.WithStack(err)
		return
	}

	if d.config.Debug {
		fmt.Println("--> ", string(jsonin))
	}

	// This is a bit of a workaround for dash v19.2 to allow wallet selection in the url
	urlAppend := ""
	if wallet, hasWallet := m["wallet"]; hasWallet {
		urlAppend = fmt.Sprintf("/wallet/%s", wallet)
		delete(m, "wallet")
	}

	req, err := http.NewRequest("POST", "http://"+d.config.Hostport+urlAppend, bytes.NewBuffer(jsonin))
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	req.Header = http.Header{"Content-Type": {echo.MIMEApplicationJSON}}
	req.SetBasicAuth(d.config.User, d.config.Pass)

	rsp, err = d.httpclient.Do(req)
	if err != nil {
		err = errors.WithStack(err)
		return
	}

	body, err := ioutil.ReadAll(rsp.Body)
	if err != nil {
		err = errors.WithStack(err)
		return
	}

	jsn = gjson.ParseBytes(body)

	if d.config.Debug {
		fmt.Println("<-- ", string(body))
	}

	if rsp.StatusCode != 200 || jsn.Get("error.code").Exists() {
		err = errors.Errorf("node communication error: %s %s", rsp.Status, string(body))
		return
	}

	return
}

# Dash Coin Flip
Send DASH to either the "heads" or "tails" address for a 50% chance to double your money.

A 1% commission is deducted from winnings and losses remain on the node. Transactions are refunded if:
- The amount sent is above or below the threshold
- There is no BLS signature (not an instant-send transaction)
- There isn't enough balance on the server to payout a win.

The coin flip result is determined by checking if the BLS signature of an instant send transaction is an even number when converted to an integer.

The bundled http api has two endpoints `/status` and `/result/:txhash` to discover addresses, win/loss statistics and view the flip result history.

A SQLite database is generated when the app is first run to store results and app state (balance + addresses).

## Requirements
A Dash node is required for receiving/sending transactions, generating addresses and balance management. The node must be configured to broadcast `rawtx`, `rawtxlocksig`, and `rawblock` events via ZMQ, and the configured ZMQ ports must be accessible by the coin flip server.

Docker and `docker-compose` are required to run the example.

Golang is required to build the standalone binary. You will also need ZMQ installed with libsodium enabled.

## Configuration
Modify the `config-reference.yml` example and rename to `config.yml` in the root directory.

The default path can be changed with the environment variable `CONFIG_PATH`.

## Running

### Docker
The docker image can be built with:
```
docker build -t dash/coinflip .
```

### Docker Compose
After building the docker image, the following commands should be all you need to startup a test Dash node along with the coin flip game server.

NOTE: The server will wait for the node to be fully-synced before starting up.

```
cd docker
docker-compose up
```

### Golang
The standalone go binary can be built and run with:
```
go build
./dash-coinflip
```

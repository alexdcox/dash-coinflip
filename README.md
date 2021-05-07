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
A Dash(v0.17+) node is required for receiving/sending transactions, generating addresses and balance management. The node must be configured to broadcast `rawtx`, `rawtxlocksig`, and `rawblock` events via ZMQ, and the configured ZMQ ports must be accessible by the coin flip server.

Docker(v20.10+) and `docker-compose`(v1.29+) are required to run the testnet example with everything pre-configured.

Golang(v1.15+) is required to build the standalone binary, which is not necessary if you take the docker route. You will also need the requirements for the [pebbe/zmq4](https://github.com/pebbe/zmq4) library and potentially gcc.

## Running
The easiest method is to clone this repo, change to the root directory, and follow the docker steps below.

### Docker
The docker image can be built with:
```
docker build -t dash/coinflip .
```

After building the docker image, the following command should be all you need to startup a test Dash node along with the coin flip game server.

```
cd docker && docker-compose up -d
```

NOTE: Please wait for the dash node to fully sync blockchain data before using the api. Navigate to `http://localhost:3000` and wait for the `dashVerificationProgress` to reach at least `0.999`% complete.

Once the dash node has fully synced blockchain data, send some Dash to the topup address to increase the server balance and finalise the setup. This can be found via the api or in the logs.

You can watch the dash node sync progress along with the server log output with either of the following commands (unfortunately there's a bug with `docker-compose logs` at the time of writing this which causes output to freeze after a short while, so these are more reliable):
```
docker logs -f --tail 200 coinflip_dash
docker logs -f --tail 200 coinflip_server
```

### Manual
Using the docker steps above is recommended (tweaking as required) as it should work for any environment, will create all docker containers/volumes/networks etc. necessary, and network everything together ready to go. That said you could build the standalone go binary and configure everything else yourself. 

To configure, copy the `config-reference.yml` example, modify to suit your setup, and rename to `config.yml` in the root directory.

Before building, you may need to install `gcc` and `libzmq` first. On Ubuntu you can build and run with:
```
apt install gcc libzmq3-dev -y
go build && ./dash-coinflip
```

The default config path can be changed with the environment variable `CONFIG_PATH`.
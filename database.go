package main

import (
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func NewDatabase(path string) (database *Database, err error) {
	logrus.Infof("Loading sqlite database from '%s'", path)

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		err = errors.WithStack(err)
		return
	}

	database = &Database{client: db}

	return
}

type Database struct {
	client *sql.DB
}

func (d *Database) Setup() (err error) {
	rows, err := d.client.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name='state'`)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	defer rows.Close()
	if rows.Next() {
		logrus.Info("Found existing database, skipping sql setup")
		return
	}

	logrus.Info("Creating sqlite database schema")

	_, err = d.client.Exec(`
		CREATE TABLE state (
		    id INTEGER PRIMARY KEY AUTOINCREMENT,
		    balance REAL,
		    heads_address TEXT,
		    tails_address TEXT,
		    topup_address TEXT,
		    won_count INTEGER,
		    lost_count INTEGER,
		    refund_count INTEGER 
		)
	`)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	_, err = d.client.Exec(`
		CREATE TABLE result (
		    id INTEGER PRIMARY KEY AUTOINCREMENT,
		    received TEXT,
		    transaction_hash TEXT,
		    transaction_from TEXT,
		    transaction_signature TEXT,
		    refund_reason TEXT,
		    refund_hash TEXT,
		    refund_error TEXT,
		    heads_amount REAL,
		    tails_amount REAL,
		    winner INTEGER,
			payout_amount REAL,
			payout_hash TEXT,
			payout_error TEXT
		)
	`)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	_, err = d.client.Exec(`
		CREATE TABLE topup (
		    id INTEGER PRIMARY KEY AUTOINCREMENT,
		    received TEXT,
		    hash TEXT,
		    "from" TEXT,
		    amount REAL
		)
	`)
	if err != nil {
		err = errors.WithStack(err)
		return
	}

	return
}

func (d *Database) StoreState(state *CoinFlipState) (err error) {
	stmt, err := d.client.Prepare(`
		INSERT INTO state(
			id,
			balance,
			heads_address,
			tails_address,
			topup_address,
			won_count,
			lost_count,
			refund_count
	    )
		VALUES(1, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO
			UPDATE SET
			    balance=?,
			    heads_address=?,
			    tails_address=?,
			    topup_address=?,
			    won_count=?,
			    lost_count=?,
			    refund_count=?
	`)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	_, err = stmt.Exec(
		state.Balance, state.HeadsAddress, state.TailsAddress, state.TopupAddress, state.WonCount, state.LostCount, state.RefundCount,
		state.Balance, state.HeadsAddress, state.TailsAddress, state.TopupAddress, state.WonCount, state.LostCount, state.RefundCount,
	)
	return errors.WithStack(err)
}

func (d *Database) GetState() (state *CoinFlipState, err error) {
	rows, err := d.client.Query(`
		SELECT
		       balance,
		       heads_address,
		       tails_address,
		       topup_address,
		       won_count,
		       lost_count,
		       refund_count
		FROM 'state' WHERE id=1`)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	defer rows.Close()
	state = new(CoinFlipState)
	for rows.Next() {
		err = errors.WithStack(rows.Scan(
			&state.Balance,
			&state.HeadsAddress,
			&state.TailsAddress,
			&state.TopupAddress,
			&state.WonCount,
			&state.LostCount,
			&state.RefundCount,
		))
	}
	return
}

func (d *Database) StoreResult(result *FlipResult) (err error) {
	stmt, err := d.client.Prepare(`
		INSERT INTO result(
			received,
			transaction_hash,
			transaction_from,
			transaction_signature,
			refund_reason,
			refund_hash,
			refund_error,
			heads_amount,
			tails_amount,
			winner,
			payout_amount,
			payout_hash,
			payout_error
		)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
	`)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	_, err = stmt.Exec(
		result.Received.Format(time.RFC3339),
		result.TransactionHash,
		result.TransactionFrom,
		result.TransactionSignature,
		result.RefundReason,
		result.RefundHash,
		result.RefundError,
		result.HeadsAmount,
		result.TailsAmount,
		result.Winner,
		result.PayoutAmount,
		result.PayoutHash,
		result.PayoutError,
	)
	return errors.WithStack(err)
}

func (d *Database) GetResult(hash string) (result *FlipResult, err error) {
	stmt, err := d.client.Prepare(`
		SELECT 
			received,
			transaction_hash,
			transaction_from,
			transaction_signature,
			refund_reason,
			refund_hash,
			refund_error,
			heads_amount,
			tails_amount,
			winner,
			payout_amount,
			payout_hash,
			payout_error
		FROM 'result'
		WHERE transaction_hash=? OR payout_hash=? OR refund_hash=?
	`)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	rows, err := stmt.Query(hash, hash, hash)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	defer rows.Close()
	result = new(FlipResult)
	if rows.Next() {
		var received string
		err = errors.WithStack(rows.Scan(
			&received,
			&result.TransactionHash,
			&result.TransactionFrom,
			&result.TransactionSignature,
			&result.RefundReason,
			&result.RefundHash,
			&result.RefundError,
			&result.HeadsAmount,
			&result.TailsAmount,
			&result.Winner,
			&result.PayoutAmount,
			&result.PayoutHash,
			&result.PayoutError,
		))
		if err != nil {
			return
		}
		result.Received, _ = time.Parse(time.RFC3339, received)
	} else {
		err = errors.Errorf("no result found for hash %s", hash)
	}
	return
}

func (d *Database) StoreTopup(topup *Topup) (err error) {
	stmt, err := d.client.Prepare(`
    INSERT INTO topup(
      received,
      hash,
      "from",
      amount
    )
    VALUES(?,?,?,?)
  `)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	_, err = stmt.Exec(
		topup.Received.Format(time.RFC3339),
		topup.Hash,
		topup.From,
		topup.Amount,
	)
	return errors.WithStack(err)
}

func (d *Database) GetTopup(hash string) (topup *Topup, err error) {
	stmt, err := d.client.Prepare(`
    SELECT 
      received,
      hash,
      "from",
      amount
    FROM 'topup'
    WHERE hash=?
  `)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	rows, err := stmt.Query(hash)
	if err != nil {
		err = errors.WithStack(err)
		return
	}
	defer rows.Close()
	topup = new(Topup)
	if rows.Next() {
		var received string
		err = errors.WithStack(rows.Scan(
			&received,
			&topup.Hash,
			&topup.From,
			&topup.Amount,
		))
		if err != nil {
			return
		}
		topup.Received, _ = time.Parse(time.RFC3339, received)
	} else {
		err = errors.Errorf("no topup found for hash %s", hash)
	}
	return
}

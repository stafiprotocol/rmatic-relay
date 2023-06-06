// Copyright 2020 Stafi Protocol
// SPDX-License-Identifier: LGPL-3.0-only

package shared

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stafiprotocol/chainbridge/utils/crypto/secp256k1"
)

var (
	BlockRetryInterval   = time.Second * 5
	DefaultGasLimit      = big.NewInt(100e4)
	DefaultExtraGasLimit = big.NewInt(15e4)

	DefaultGasPrice   = big.NewInt(50e9) //50gwei
	lowExtraGasPrice  = big.NewInt(2e9)  // 5gwei
	highExtraGasPrice = big.NewInt(5e9)  //5gwei
	standGasPrice     = big.NewInt(20e9) //20gwei
)

type Client struct {
	endpoint    string
	kp          *secp256k1.Keypair
	gasLimit    *big.Int
	maxGasPrice *big.Int
	conn        *ethclient.Client
	opts        *bind.TransactOpts
	nonce       uint64
	optsLock    sync.Mutex
}

// NewClient returns an uninitialized connection, must call Client.Connect() before using.
func NewClient(endpoint string, kp *secp256k1.Keypair, gasLimit, maxGasPrice *big.Int) (*Client, error) {
	client := &Client{
		endpoint:    endpoint,
		kp:          kp,
		gasLimit:    gasLimit,
		maxGasPrice: maxGasPrice,
	}

	if client.gasLimit == nil || client.gasLimit.Uint64() == 0 {
		client.gasLimit = DefaultGasLimit
	}

	if client.maxGasPrice == nil || client.maxGasPrice.Uint64() == 0 {
		client.maxGasPrice = DefaultGasPrice
	}

	err := client.connect()
	if err != nil {
		return nil, err
	}

	return client, nil
}

// Connect starts the ethereum WS connection
func (c *Client) connect() error {
	client, err := ethclient.Dial(c.endpoint)
	if err != nil {
		return err
	}

	c.conn = client

	var chainId *big.Int
	retry := 0
	for {
		if retry > 50 {
			return fmt.Errorf("get chainId err: %s", err)
		}
		chainId, err = c.conn.ChainID(context.Background())
		if err != nil {
			retry++
			time.Sleep(time.Second * 3)
			continue
		}
		break
	}

	// Construct tx opts, call opts, and nonce mechanism
	if c.kp != nil {
		opts, _, err := c.newTransactOpts(big.NewInt(0), c.gasLimit, c.maxGasPrice, chainId)
		if err != nil {
			return err
		}
		c.opts = opts
		c.nonce = 0
	}
	return nil
}

// newTransactOpts builds the TransactOpts for the connection's keypair.
func (c *Client) newTransactOpts(value, gasLimit, gasPrice *big.Int, chainId *big.Int) (*bind.TransactOpts, uint64, error) {
	privateKey := c.kp.PrivateKey()
	address := crypto.PubkeyToAddress(privateKey.PublicKey)

	nonce, err := c.conn.NonceAt(context.Background(), address, nil)
	if err != nil {
		return nil, 0, err
	}
	opts, err := bind.NewKeyedTransactorWithChainID(privateKey, chainId)
	if err != nil {
		return nil, 0, err
	}
	opts.Nonce = big.NewInt(int64(nonce))
	opts.Value = value
	opts.GasLimit = uint64(gasLimit.Int64())
	opts.GasPrice = gasPrice
	opts.Context = context.Background()

	return opts, nonce, nil
}

func (c *Client) Keypair() *secp256k1.Keypair {
	return c.kp
}

func (c *Client) Client() *ethclient.Client {
	return c.conn
}

func (c *Client) Opts() *bind.TransactOpts {
	return c.opts
}

func (c *Client) safeEstimateGas(ctx context.Context) (*big.Int, error) {
	gasPrice, err := c.conn.SuggestGasPrice(context.TODO())
	if err != nil {
		return nil, err
	}

	if gasPrice.Cmp(standGasPrice) > 0 {
		gasPrice = gasPrice.Add(gasPrice, highExtraGasPrice)
	} else {
		gasPrice = gasPrice.Add(gasPrice, lowExtraGasPrice)
	}

	if gasPrice.Cmp(c.maxGasPrice) > 0 {
		gasPrice = c.maxGasPrice
	}

	return gasPrice, nil
}

// LockAndUpdateOpts acquires a lock on the opts before updating the nonce
// and gas price.
func (c *Client) LockAndUpdateOpts(gasLimit, value *big.Int) error {
	c.optsLock.Lock()

	gasPrice, err := c.safeEstimateGas(context.TODO())
	if err != nil {
		c.optsLock.Unlock()
		return err
	}
	c.opts.GasPrice = gasPrice

	nonce, err := c.conn.PendingNonceAt(context.Background(), c.opts.From)
	if err != nil {
		c.optsLock.Unlock()
		return err
	}
	c.opts.Nonce.SetUint64(nonce)

	if gasLimit.Uint64() == 0 {
		c.opts.GasLimit = DefaultGasLimit.Uint64()
	} else {
		c.opts.GasLimit = new(big.Int).Add(gasLimit, DefaultExtraGasLimit).Uint64()
	}

	c.opts.Value = value
	return nil
}

func (c *Client) UnlockOpts() {
	c.opts.Value = big.NewInt(0)
	c.optsLock.Unlock()
}

// LatestBlock returns the latest block from the current chain
func (c *Client) LatestBlock() (*big.Int, error) {
	header, err := c.conn.HeaderByNumber(context.Background(), nil)
	if err != nil {
		return nil, err
	}

	return header.Number, nil
}

// LatestBlock returns the latest block from the current chain
func (c *Client) LatestBlockTimestamp() (uint64, error) {
	header, err := c.conn.HeaderByNumber(context.Background(), nil)
	if err != nil {
		return 0, err
	}

	return header.Time, nil
}

func (c *Client) LatestBlockAndTimestamp() (uint64, uint64, error) {
	header, err := c.conn.HeaderByNumber(context.Background(), nil)
	if err != nil {
		return 0, 0, err
	}

	return header.Number.Uint64(), header.Time, nil
}

// EnsureHasBytecode asserts if contract code exists at the specified address
func (c *Client) EnsureHasBytecode(addr common.Address) error {
	code, err := c.conn.CodeAt(context.Background(), addr, nil)
	if err != nil {
		return err
	}

	if len(code) == 0 {
		return fmt.Errorf("no bytecode found at %s", addr.Hex())
	}
	return nil
}

func (c *Client) TransactionReceipt(hash common.Hash) (*types.Receipt, error) {
	return c.conn.TransactionReceipt(context.Background(), hash)
}

func (c *Client) TransactionByHash(hash common.Hash) (tx *types.Transaction, isPending bool, err error) {
	return c.conn.TransactionByHash(context.Background(), hash)
}

func (c *Client) TransactionSender(ctx context.Context, tx *types.Transaction, block common.Hash, index uint) (common.Address, error) {
	return c.conn.TransactionSender(ctx, tx, block, index)
}

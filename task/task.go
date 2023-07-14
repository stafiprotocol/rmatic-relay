package task

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"
	"github.com/stafiprotocol/chainbridge/utils/crypto/secp256k1"
	"rmatic-relay/bindings/StakeManager"
	"rmatic-relay/bindings/StakePortalRate"
	"rmatic-relay/pkg/config"
	"rmatic-relay/pkg/utils"
	"rmatic-relay/shared"
)

type Task struct {
	taskTicker         int64
	stop               chan struct{}
	ethRpcEndpoint     string
	polygonRpcEndpoint string
	keyPair            *secp256k1.Keypair
	gasLimit           *big.Int
	maxGasPrice        *big.Int

	ethStakeMangerAddress         common.Address
	polygonStakePortalRateAddress common.Address

	// need init on start()
	isDev bool

	ethClient                      *shared.Client
	polygonClient                  *shared.Client
	ethContractStakeManager        *stake_manager.StakeManager
	polygonContractStakePortalRate *stake_portal_rate.StakePortalRate
}

func NewTask(cfg *config.Config, keyPair *secp256k1.Keypair) (*Task, error) {
	gasLimitDeci, err := decimal.NewFromString(cfg.GasLimit)
	if err != nil {
		return nil, err
	}

	if gasLimitDeci.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("gas limit is zero")
	}
	maxGasPriceDeci, err := decimal.NewFromString(cfg.MaxGasPrice)
	if err != nil {
		return nil, err
	}
	if maxGasPriceDeci.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("max gas price is zero")
	}

	s := &Task{
		taskTicker:                    15,
		stop:                          make(chan struct{}),
		ethRpcEndpoint:                cfg.EthRpcEndpoint,
		polygonRpcEndpoint:            cfg.PolygonRpcEndpoint,
		keyPair:                       keyPair,
		gasLimit:                      gasLimitDeci.BigInt(),
		maxGasPrice:                   maxGasPriceDeci.BigInt(),
		ethStakeMangerAddress:         common.HexToAddress(cfg.StakeMangerAddress),
		polygonStakePortalRateAddress: common.HexToAddress(cfg.PolygonStakePortalRateAddress),
	}

	return s, nil
}

func (task *Task) Start() error {
	ethClient, err := shared.NewClient(task.ethRpcEndpoint, task.keyPair, task.gasLimit, task.maxGasPrice)
	if err != nil {
		return err
	}
	task.ethClient = ethClient

	polygonClient, err := shared.NewClient(task.polygonRpcEndpoint, task.keyPair, task.gasLimit, task.maxGasPrice)
	if err != nil {
		return err
	}
	task.polygonClient = polygonClient

	chainId, err := task.ethClient.Client().ChainID(context.Background())
	if err != nil {
		return err
	}
	switch chainId.Uint64() {
	case 1:
		task.isDev = false
	case 5:
		task.isDev = true
	default:
		return fmt.Errorf("unsupport chainId: %d", chainId.Int64())
	}

	stakeManger, err := stake_manager.NewStakeManager(task.ethStakeMangerAddress, task.ethClient.Client())
	if err != nil {
		return err
	}
	bondedPools, err := stakeManger.GetBondedPools(&bind.CallOpts{
		Context: context.Background(),
	})
	if err != nil {
		return err
	}
	if len(bondedPools) == 0 {
		return fmt.Errorf("no bonded pools")
	}
	task.ethContractStakeManager = stakeManger

	stakePortalRate, err := stake_portal_rate.NewStakePortalRate(task.polygonStakePortalRateAddress, task.polygonClient.Client())
	if err != nil {
		return err
	}
	task.polygonContractStakePortalRate = stakePortalRate

	utils.SafeGoWithRestart(task.newEraHandler)
	utils.SafeGoWithRestart(task.syncRateHandler)
	return nil
}

func (task *Task) Stop() {
	close(task.stop)
}

func (task *Task) newEraHandler() {
	logrus.Info("start new era Handler")
	ticker := time.NewTicker(time.Duration(task.taskTicker) * time.Second)
	defer ticker.Stop()

	for {

		select {
		case <-task.stop:
			logrus.Info("task has stopped")
			return
		case <-ticker.C:
			logrus.Debug("newEraHandler start -----------")
			err := task.handleNewEra()
			if err != nil {
				logrus.Warnf("newEraHandler failed, err: %s", err.Error())
				continue
			}
			logrus.Debug("newEraHandler end -----------")
		}
	}
}
func (task *Task) syncRateHandler() {
	logrus.Info("start sync rate Handler")
	ticker := time.NewTicker(time.Duration(task.taskTicker) * time.Second)
	defer ticker.Stop()

	for {

		select {
		case <-task.stop:
			logrus.Info("task has stopped")
			return
		case <-ticker.C:
			logrus.Debug("syncRMaticRateHandler start -----------")
			err := task.syncRMaticRateHandler()
			if err != nil {
				logrus.Warnf("syncRMaticRateHandler failed, err: %s", err.Error())
				continue
			}
			logrus.Debug("syncRMaticRateHandler end -----------")
		}
	}
}

func (task *Task) waitTxOnChain(txHash common.Hash, client *shared.Client) (err error) {
	retry := 0
	txSuccess := false
	for {
		if retry > utils.RetryLimit {
			return fmt.Errorf("waitTxOnChain %s reach retry limit", txHash.String())
		}
		_, pending, err := client.TransactionByHash(txHash)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"hash": txHash.String(),
				"err":  err.Error(),
			}).Warn("TransactionByHash")

			time.Sleep(utils.RetryInterval)
			retry++
			continue
		} else {
			if pending {
				logrus.WithFields(logrus.Fields{
					"hash":    txHash.String(),
					"pending": pending,
				}).Warn("TransactionByHash")

				time.Sleep(utils.RetryInterval)
				retry++
				continue
			} else {
				// check status
				var receipt *types.Receipt
				subRetry := 0
				for {
					if subRetry > utils.RetryLimit {
						return fmt.Errorf("TransactionReceipt %s reach retry limit", txHash.String())
					}

					receipt, err = task.ethClient.TransactionReceipt(txHash)
					if err != nil {
						logrus.WithFields(logrus.Fields{
							"hash": txHash.String(),
							"err":  err.Error(),
						}).Warn("tx TransactionReceipt")

						time.Sleep(utils.RetryInterval)
						subRetry++
						continue
					}
					break
				}

				if receipt.Status == 1 { //success
					txSuccess = true
				}
				break
			}
		}
	}

	logrus.WithFields(logrus.Fields{
		"tx":         txHash.String(),
		"tx success": txSuccess,
	}).Info("tx already on chain")

	return nil
}

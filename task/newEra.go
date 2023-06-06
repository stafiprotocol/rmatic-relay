package task

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func (t *Task) newEraHandler() error {
	latestCallOpts := bind.CallOpts{
		Pending: false,
		From:    [20]byte{},
		Context: context.Background(),
	}

	currentEra, err := t.contractStakeManager.CurrentEra(&latestCallOpts)
	if err != nil {
		return err
	}
	latestEra, err := t.contractStakeManager.LatestEra(&latestCallOpts)
	if err != nil {
		return err
	}

	err = t.checkAndCallNewEra(currentEra, latestEra, &latestCallOpts)
	if err != nil {
		return err
	}

	return nil
}

func (t *Task) checkAndCallNewEra(currentEra, latestEra *big.Int, latestCallOpts *bind.CallOpts) error {
	// case 0: currentEra==latestEra
	// no need deal
	if currentEra.Cmp(latestEra) == 0 {
		logrus.Debug("currentEra==latestEra no need deal")
		return nil
	}

	// case 1: currentEra > latestEra
	// vote newEra
	willUseEra := new(big.Int).Add(latestEra, big.NewInt(1))

	// check era
	latestEra, err := t.contractStakeManager.LatestEra(latestCallOpts)
	if err != nil {
		return err
	}
	if willUseEra.Cmp(new(big.Int).Add(latestEra, big.NewInt(1))) != 0 {
		logrus.Debugf("willUseEra: %d not match latestEra: %d, no need deal", willUseEra.Int64(), latestEra.Int64())
		return nil
	}

	// send tx
	err = t.ethClient.LockAndUpdateOpts(t.gasLimit, big.NewInt(0))
	if err != nil {
		return err
	}
	tx, err := t.contractStakeManager.NewEra(t.ethClient.Opts())
	t.ethClient.UnlockOpts()
	if err != nil {
		return err
	}

	err = t.waitTxOnChain(tx.Hash())
	if err != nil {
		return errors.Wrap(err, "waitTxOnChain failed")
	}

	//wait until newEra executed
	retry := 0
	for {
		// wait 2h
		if retry > 2*60*5 {
			return fmt.Errorf("wait newEra executed failed")
		}
		latestEra, err := t.contractStakeManager.LatestEra(latestCallOpts)
		if err != nil {
			logrus.Warnf("get latestEra failed: %s", err.Error())
			time.Sleep(12 * time.Second)
			retry++
			continue
		}

		if latestEra.Cmp(willUseEra) < 0 {
			logrus.Warnf("waiting newEra executed...")
			time.Sleep(12 * time.Second)
			retry++
			continue
		}
		logrus.Info("newEra already executed success")
		break
	}

	return nil
}

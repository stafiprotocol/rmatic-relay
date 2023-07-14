package task

import (
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/sirupsen/logrus"
	"rmatic-relay/bindings/StakePortalRate"
	"rmatic-relay/shared"
)

func (t Task) syncRMaticRateHandler() error {
	rateOnEth, err := t.ethContractStakeManager.GetRate(nil)
	if err != nil {
		logrus.Warnf("ethStakeManager.GetRate failed, err: %s", err.Error())
		return err
	}

	rateOnPolygon, err := t.polygonContractStakePortalRate.GetRate(nil)
	if err != nil {
		logrus.Warnf("polygonStakePortalRate.GetRate failed, err: %s", err.Error())
		return err
	}

	if rateOnEth.Cmp(rateOnPolygon) == 0 {
		return nil
	}

	latestEra, err := t.ethContractStakeManager.LatestEra(nil)
	if err != nil {
		logrus.Warnf("ethStakeManager.LatestEra failed, err: %s", err.Error())
		return err
	}
	proposalId := getProposalId(uint32(latestEra.Uint64()), rateOnEth, 0)
	err = polygonVoteRate(t.polygonContractStakePortalRate, proposalId, rateOnEth, t.polygonClient)
	if err != nil {
		logrus.Warnf("polygonVoteRate failed, err: %s", err.Error())
		return err
	}
	return nil
}

func polygonVoteRate(polygonStakePortalRateContract *stake_portal_rate.StakePortalRate, proposalId [32]byte, evmRate *big.Int, polygonConn *shared.Client) error {
	proposal, err := polygonStakePortalRateContract.Proposals(nil, proposalId)
	if err != nil {
		return fmt.Errorf("processSignatureEnough Proposals error %s ", err)
	}
	if proposal.Status == 2 { // success status
		return nil
	}
	hasVoted, err := polygonStakePortalRateContract.HasVoted(&bind.CallOpts{}, proposalId, polygonConn.Opts().From)
	if err != nil {
		return fmt.Errorf("processSignatureEnough HasVoted error %s", err)
	}
	if hasVoted {
		return nil
	}

	// send tx
	err = polygonConn.LockAndUpdateOpts(big.NewInt(0), big.NewInt(0))
	if err != nil {
		return fmt.Errorf("processSignatureEnough LockAndUpdateOpts error %s", err)
	}
	polygonConn.UnlockOpts()

	voteTx, err := polygonStakePortalRateContract.VoteRate(polygonConn.Opts(), proposalId, evmRate)
	if err != nil {
		return fmt.Errorf("processSignatureEnough VoteRate error %s", err)
	}

	err = waitPolygonTxOk(voteTx.Hash(), polygonConn)
	if err != nil {
		return fmt.Errorf("processSignatureEnough waitTxOk error %s", err)
	}

	err = waitPolygonRateUpdated(polygonStakePortalRateContract, proposalId)
	if err != nil {
		return fmt.Errorf("processSignatureEnough waitRateUpdated error %s", err)
	}
	return nil
}

func getProposalId(era uint32, rate *big.Int, factor int) common.Hash {
	return crypto.Keccak256Hash([]byte(fmt.Sprintf("era-%d-%s-%s-%d", era, "voteRate", rate.String(), factor)))
}

func waitPolygonTxOk(txHash common.Hash, polygonConn *shared.Client) error {
	retry := 0
	for {
		if retry > 300 {
			return fmt.Errorf("waitPolygonTxOk tx reach retry limit")
		}
		_, pending, err := polygonConn.TransactionByHash(txHash)
		if err == nil && !pending {
			break
		} else {
			if err != nil {
				logrus.WithFields(logrus.Fields{
					"tx hash": txHash,
					"err":     err.Error(),
				}).Warn("tx status")
			} else {
				logrus.WithFields(logrus.Fields{
					"tx hash": txHash,
					"status":  "pending",
				}).Warn("tx status")
			}
			time.Sleep(6 * time.Second)
			retry++
			continue
		}

	}
	logrus.WithFields(logrus.Fields{
		"tx hash": txHash,
	}).Info("tx send ok")
	return nil
}

func waitPolygonRateUpdated(polygonStakePortalRateContract *stake_portal_rate.StakePortalRate, proposalId [32]byte) error {
	retry := 0
	for {
		if retry > 300 {
			return fmt.Errorf("waitPolygonRateUpdated tx reach retry limit")
		}

		proposal, err := polygonStakePortalRateContract.Proposals(&bind.CallOpts{}, proposalId)
		if err != nil {
			time.Sleep(6 * time.Second)
			retry++
			continue
		}
		if proposal.Status != 2 {
			time.Sleep(6 * time.Second)
			retry++
			continue
		}
		break
	}
	return nil
}

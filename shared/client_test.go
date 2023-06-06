package shared_test

import (
	"context"
	"math/big"
	"rmatic-relay/shared"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestClient(t *testing.T) {
	client, err := shared.NewClient("https://data-seed-prebsc-1-s2.binance.org:8545", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	txRes, err := client.TransactionReceipt(common.HexToHash("0x07c18753e3603abc16981658519ebaa98004f8e5b1072b6dfc614f1a020f75c3"))
	if err != nil {
		t.Fatal(err)
	}

	t.Log(txRes.BlockHash)

	block, err := client.Client().BlockByNumber(context.Background(), big.NewInt(30265875))
	if err != nil {
		t.Fatal(err)
	}

	t.Log(block.Time())
}

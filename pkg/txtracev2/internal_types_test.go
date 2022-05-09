package txtracev2

import (
	"bytes"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rlp"
)

func TestCodec(t *testing.T) {
	internal_Action := InternalAction{
		CallType:      uint8(1),
		From:          &common.Address{},
		To:            nil,
		Value:         big.NewInt(0),
		Gas:           uint64(1),
		Init:          []byte{},
		Input:         []byte{},
		Address:       nil,
		RefundAddress: nil,
		Balance:       big.NewInt(0),
	}
	internal_TraceActionResult := InternalTraceActionResult{
		GasUsed: uint64(1),
		Output:  []byte{},
		Code:    []byte{},
		Address: nil,
	}
	internal_ActionTrace := InternalActionTrace{
		Action:       internal_Action,
		Result:       &internal_TraceActionResult,
		Subtraces:    uint32(1),
		TraceAddress: make([]uint32, 0),
	}
	internal_ActionTraces := InternalActionTraces{
		Traces:              []InternalActionTrace{internal_ActionTrace},
		BlockHash:           common.Hash{},
		BlockNumber:         big.NewInt(0),
		TransactionHash:     common.Hash{},
		TransactionPosition: uint64(1),
	}
	raw, err := rlp.EncodeToBytes(&internal_ActionTraces)
	if err != nil {
		t.Fatalf("rlp.EncodeToBytes failed: %v", err)
	}
	outPut := &InternalActionTraces{}
	err = rlp.DecodeBytes(raw, outPut)
	if err != nil {
		t.Fatalf("rlp.DecodeBytes failed: %v", err)
	}
	t.Logf("inPut:  %+v", internal_ActionTraces)
	t.Logf("inPut.ToRpcTraces:  %+v", internal_ActionTraces.ToRpcTraces())
	t.Logf("outPut: %+v", *outPut)
	t.Logf("outPut.ToRpcTraces: %+v", outPut.ToRpcTraces())

	j1, _ := json.Marshal(&internal_ActionTraces)
	j2, _ := json.Marshal(outPut)
	if !bytes.Equal(j1, j2) {
		t.Fatalf("json.Marshal failed: %s:%s", string(j1), string(j2))
	}
}

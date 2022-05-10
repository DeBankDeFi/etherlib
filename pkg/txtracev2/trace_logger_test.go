package txtracev2

import (
	"context"
	"encoding/json"
	"errors"
	"io/ioutil"
	"math/big"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/tests"
)

type callContext struct {
	Number     math.HexOrDecimal64   `json:"number"`
	Difficulty *math.HexOrDecimal256 `json:"difficulty"`
	Time       math.HexOrDecimal64   `json:"timestamp"`
	GasLimit   math.HexOrDecimal64   `json:"gasLimit"`
	Miner      common.Address        `json:"miner"`
}

// callTracerTest defines a single test to check the call tracer against.
type callTracerTest struct {
	Genesis *core.Genesis `json:"genesis"`
	Context *callContext  `json:"context"`
	Input   string        `json:"input"`
	Result  []ActionTrace `json:"result"`
}

type MemoryStore struct {
	data map[common.Hash][]byte
}

func (store *MemoryStore) ReadTxTrace(ctx context.Context, txHash common.Hash) ([]byte, error) {
	if raw, isExist := store.data[txHash]; isExist {
		return raw, nil
	}
	return nil, errors.New("tx not found")
}

func (store *MemoryStore) WriteTxTrace(ctx context.Context, txHash common.Hash, trace []byte) error {
	store.data[txHash] = trace
	return nil
}

// Iterates over all the input-output datasets in the tracer test harness and
// runs the JavaScript tracers against them.
func TestCallTracer(t *testing.T) {
	files, err := ioutil.ReadDir("testdata")
	if err != nil {
		t.Fatalf("failed to retrieve tracer test suite: %v", err)
	}
	for _, file := range files {
		if !strings.HasPrefix(file.Name(), "call_tracer_") {
			continue
		}
		file := file // capture range variable
		t.Run(camel(strings.TrimSuffix(strings.TrimPrefix(file.Name(), "call_tracer"), ".json")), func(t *testing.T) {
			t.Parallel()

			// Call tracer test found, read if from disk
			blob, err := ioutil.ReadFile(filepath.Join("testdata", file.Name()))
			if err != nil {
				t.Fatalf("failed to read testcase: %v", err)
			}
			test := new(callTracerTest)
			if err := json.Unmarshal(blob, test); err != nil {
				t.Fatalf("failed to parse testcase: %v", err)
			}
			// Configure a blockchain with the given prestate
			tx := new(types.Transaction)
			if err := rlp.DecodeBytes(common.FromHex(test.Input), tx); err != nil {
				t.Fatalf("failed to parse testcase input: %v", err)
			}
			signer := types.MakeSigner(test.Genesis.Config, new(big.Int).SetUint64(uint64(test.Context.Number)))
			origin, _ := signer.Sender(tx)

			blkContext := vm.BlockContext{
				CanTransfer: core.CanTransfer,
				Transfer:    core.Transfer,
				Coinbase:    test.Context.Miner,
				GasLimit:    uint64(test.Context.GasLimit),
				BlockNumber: new(big.Int).SetUint64(uint64(test.Context.Number)),
				Time:        new(big.Int).SetUint64(uint64(test.Context.Time)),
				Difficulty:  (*big.Int)(test.Context.Difficulty),
			}
			txContext := vm.TxContext{
				Origin:   origin,
				GasPrice: tx.GasPrice(),
			}

			_, statedb := tests.MakePreState(rawdb.NewMemoryDatabase(), test.Genesis.Alloc, false)

			memoryStore := &MemoryStore{
				data: make(map[common.Hash][]byte),
			}

			// Create the tracer, the EVM environment and run it
			tracer := NewOeTracer(memoryStore, common.Hash{}, new(big.Int).SetUint64(uint64(test.Context.Number)), tx.Hash(), 0)

			evm := vm.NewEVM(blkContext, txContext, statedb, test.Genesis.Config, vm.Config{Debug: true, Tracer: tracer})

			msg, err := tx.AsMessage(signer, nil)
			if err != nil {
				t.Fatalf("failed to prepare transaction for tracing: %v", err)
			}

			st := core.NewStateTransition(evm, msg, new(core.GasPool).AddGas(tx.Gas()))
			if _, err = st.TransitionDb(); err != nil {
				t.Fatalf("failed to execute transaction: %v", err)
			}
			res := tracer.GetTraces()
			if !jsonEqual(res, test.Result) {
				jsonDiff(t, res, test.Result)
			}

			tracer.PersistTrace()

			storeRes, err := ReadRpcTxTrace(memoryStore, context.Background(), tx.Hash())
			if err != nil {
				t.Logf("failed to read trace: %v", err)
			}
			if !jsonEqual(storeRes, test.Result) {
				jsonDiff(t, storeRes, test.Result)
			}

		})
	}
}

func jsonDiff(t *testing.T, x, y interface{}) {
	xj, _ := json.Marshal(x)
	yj, _ := json.Marshal(y)
	t.Fatalf("trace mismatch: \nhave %+v\nwant %+v", string(xj), string(yj))
}

// jsonEqual is similar to reflect.DeepEqual, but does a 'bounce' via json prior to
// comparison
func jsonEqual(x, y interface{}) bool {
	xTrace := make(ActionTraceList, 0)
	yTrace := make(ActionTraceList, 0)
	if xj, err := json.Marshal(x); err == nil {
		_ = json.Unmarshal(xj, &xTrace)
	} else {
		return false
	}
	if yj, err := json.Marshal(y); err == nil {
		_ = json.Unmarshal(yj, &yTrace)
	} else {
		return false
	}
	return reflect.DeepEqual(xTrace, yTrace)
}

// camel converts a snake cased input string into a camel cased output.
func camel(str string) string {
	pieces := strings.Split(str, "_")
	for i := 1; i < len(pieces); i++ {
		pieces[i] = string(unicode.ToUpper(rune(pieces[i][0]))) + pieces[i][1:]
	}
	return strings.Join(pieces, "")
}

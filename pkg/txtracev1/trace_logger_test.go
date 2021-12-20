package txtrace

import (
	"encoding/json"
	"fmt"
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
	Genesis *core.Genesis  `json:"genesis"`
	Context *callContext   `json:"context"`
	Input   string         `json:"input"`
	Result  *[]ActionTrace `json:"result"`
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

			// Create the tracer, the EVM environment and run it
			tracer := NewTraceStructLogger(nil)

			evm := vm.NewEVM(blkContext, txContext, statedb, test.Genesis.Config, vm.Config{Debug: true, Tracer: tracer})

			msg, err := tx.AsMessage(signer, nil)
			if err != nil {
				t.Fatalf("failed to prepare transaction for tracing: %v", err)
			}

			tracer.SetBlockNumber(new(big.Int).SetUint64(uint64(test.Context.Number)))
			tracer.SetFrom(msg.From())
			tracer.SetTo(msg.To())
			tracer.SetValue(*msg.Value())
			tracer.SetTx(tx.Hash())
			// fmt.Println(msg.From(), msg.To(), msg.Nonce(), msg.Value(), msg.GasPrice(), msg.Gas(), string(msg.Data()))
			st := core.NewStateTransition(evm, msg, new(core.GasPool).AddGas(tx.Gas()))
			if _, err = st.TransitionDb(); err != nil {
				t.Fatalf("failed to execute transaction: %v", err)
			}
			// Retrieve the trace result and compare against the etalon
			tracer.Finalize()
			res := tracer.GetResult()
			// var buf bytes.Buffer
			// err = json.NewEncoder(&buf).Encode(&res)
			// if err != nil {
			// 	t.Fatalf(err.Error())
			// }
			//
			// fmt.Println(buf.String())

			if !jsonEqual(res, test.Result) {
				jsonDiff(t, res, test.Result)
			}
		})
	}
}

func jsonDiff(t *testing.T, x, y interface{}) {
	xj, _ := json.Marshal(x)
	yj, _ := json.Marshal(y)
	// t.Fatalf("trace mismatch: \nhave %+v\nwant %+v", res, test.Result)
	t.Fatalf("trace mismatch: \nhave %+v\nwant %+v", string(xj), string(yj))
}

// jsonEqual is similar to reflect.DeepEqual, but does a 'bounce' via json prior to
// comparison
func jsonEqual(x, y interface{}) bool {
	xTrace := new([]ActionTrace)
	yTrace := new([]ActionTrace)
	if xj, err := json.Marshal(x); err == nil {
		_ = json.Unmarshal(xj, xTrace)
	} else {
		return false
	}
	if yj, err := json.Marshal(y); err == nil {
		_ = json.Unmarshal(yj, yTrace)
	} else {
		return false
	}
	return reflect.DeepEqual(xTrace, yTrace)
}

// rlpEqual is similar to reflect.DeepEqual, but does a 'bounce' via json prior to
// comparison
func rlpEqual(actual, expected interface{}) bool {
	actualTrace := new(ActionTraces)
	expectedTrace := new(ActionTraces)
	if aj, err := rlp.EncodeToBytes(actual); err != nil {
		_ = rlp.DecodeBytes(aj, actualTrace)
	} else {
		return false
	}
	if ej, err := rlp.EncodeToBytes(expected); err != nil {
		_ = rlp.DecodeBytes(ej, expectedTrace)
	} else {
		return false
	}
	return reflect.DeepEqual(actual, expectedTrace)
}

// camel converts a snake cased input string into a camel cased output.
func camel(str string) string {
	pieces := strings.Split(str, "_")
	for i := 1; i < len(pieces); i++ {
		pieces[i] = string(unicode.ToUpper(rune(pieces[i][0]))) + pieces[i][1:]
	}
	return strings.Join(pieces, "")
}

type traceActionsTest struct {
	Origin []ActionTrace
	Result []ActionTrace
}

func TestCompareRLPAndJSONEncodedSize(t *testing.T) {
	blob, err := ioutil.ReadFile(filepath.Join("testdata", "trace_actions_decode_deep_calls.json"))
	if err != nil {
		t.Fatalf("failed to read testcase: %v", err)
	}

	test := new(traceActionsTest)
	err = json.Unmarshal(blob, test)
	if err != nil {
		t.Fatalf("failed to decode testcase: %v", err)
	}

	var tester ActionTraces
	tester = test.Origin

	rlpBytes, err := rlp.EncodeToBytes(&tester)
	if err != nil {
		t.Fatalf("rlp: falied to encode action tracer: %v", err)
	}

	jsonBytes, err := json.Marshal(&test.Origin)
	if err != nil {
		t.Fatalf("json: falied to encode action tracer: %v", err)
	}

	fmt.Printf("\nrlp encoded size: \t%d\njson encoded size: \t%d\n", len(rlpBytes), len(jsonBytes))
}

func TestTraceActionsEncode(t *testing.T) {
	files, err := ioutil.ReadDir("testdata")
	if err != nil {
		t.Fatalf("failed to retrieve tracer test suite: %v", err)
	}

	for _, file := range files {
		if !strings.HasPrefix(file.Name(), "trace_actions_decode") {
			continue
		}
		file := file // capture range variable

		t.Run(camel(strings.TrimSuffix(strings.TrimPrefix(file.Name(), "trace_actions_decode_"), ".json")), func(t *testing.T) {
			t.Parallel()

			blob, err := ioutil.ReadFile(filepath.Join("testdata", file.Name()))
			if err != nil {
				t.Fatalf("failed to read testcase: %v", err)
			}

			test := new(traceActionsTest)
			err = json.Unmarshal(blob, test)
			if err != nil {
				t.Fatalf("failed to decode testcase: %v", err)
			}

			var actions ActionTraces
			actions = test.Origin
			// encode to bytes
			actionsBytes, err := rlp.EncodeToBytes(&actions)
			if err != nil {
				t.Fatalf("failed to encode actions: %v", err)
			}
			// decode the encoded actions bytes to actions
			newActions := new(ActionTraces)
			err = rlp.DecodeBytes(actionsBytes, newActions)
			if err != nil {
				t.Fatalf("failed to decode bytes to actions: %v", err)
			}

			var results ActionTraces
			results = test.Result
			if !jsonEqual(newActions, results) {
				x, _ := json.Marshal(*newActions)
				y, _ := json.Marshal(results)
				t.Logf("\nhave: %s\nwant: %s\n", string(x), string(y))
				t.Fail()
			}
		})
	}
}

func BenchmarkActionTraces_EncodeRLP(b *testing.B) {
	blob, err := ioutil.ReadFile(filepath.Join("testdata", "trace_actions_decode_create.json"))
	if err != nil {
		b.Fatalf("failed to read testcase: %v", err)
	}

	test := new(traceActionsTest)
	err = json.Unmarshal(blob, test)
	if err != nil {
		b.Fatalf("failed to decode testcase: %v", err)
	}

	var tester ActionTraces
	tester = test.Origin
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := rlp.EncodeToBytes(&tester)
		if err != nil {
			b.Fatalf("failed to encode action traces: %v", err)
		}
	}
}

func BenchmarkActionTraces_EncodeJSON(b *testing.B) {
	blob, err := ioutil.ReadFile(filepath.Join("testdata", "trace_actions_decode_create.json"))
	if err != nil {
		b.Fatalf("failed to read testcase: %v", err)
	}

	test := new(traceActionsTest)
	err = json.Unmarshal(blob, test)
	if err != nil {
		b.Fatalf("failed to decode testcase: %v", err)
	}

	tester := test.Origin
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&tester)
		if err != nil {
			b.Fatalf("failed to encode action traces: %v", err)
		}
	}
}

func BenchmarkActionTraces_DecodeRLP(b *testing.B) {
	blob, err := ioutil.ReadFile(filepath.Join("testdata", "trace_actions_decode_create.json"))
	if err != nil {
		b.Fatalf("failed to read testcase: %v", err)
	}

	test := new(traceActionsTest)
	err = json.Unmarshal(blob, test)
	if err != nil {
		b.Fatalf("failed to decode testcase: %v", err)
	}

	var tester ActionTraces
	tester = test.Origin
	bs, err := rlp.EncodeToBytes(&tester)
	if err != nil {
		b.Fatalf("failed to encode action traces: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		actionTraces := new(ActionTraces)
		err := rlp.DecodeBytes(bs, actionTraces)
		if err != nil {
			b.Fatalf("failed to encode action traces: %v", err)
		}
	}
}

func BenchmarkActionTraces_DecodeJSON(b *testing.B) {
	blob, err := ioutil.ReadFile(filepath.Join("testdata", "trace_actions_decode_create.json"))
	if err != nil {
		b.Fatalf("failed to read testcase: %v", err)
	}

	test := new(traceActionsTest)
	err = json.Unmarshal(blob, test)
	if err != nil {
		b.Fatalf("failed to decode testcase: %v", err)
	}

	bs, err := json.Marshal(&test.Origin)
	if err != nil {
		b.Fatalf("failed to encode action traces: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		actionTraces := new(ActionTraces)
		err := json.Unmarshal(bs, actionTraces)
		if err != nil {
			b.Fatalf("failed to encode action traces: %v", err)
		}
	}
}

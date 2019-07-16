package htlc

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/walker1992/atomicswap/utils"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const hourSeconds = 3600
const oneFinney = 1000000000000000
const port = "7545"
const REQUIRE_FAILED_MSG = "VM Exception while processing transaction: revert"
const REQUIRE_FAILED_TX_MSG = "the tx doesn't have the correct nonce"
const senderKey = "a5a1aca01671e2660f1ee47abfd7065d5d38f99fa4a53495f02df939cd5b86f6"
const receiverKey = "08cd4fde21e980c7d05afa3b0d4d27534e646be3cc3a67b303b055d1166cbae3"
const someGuyKey = "b33103e4d30d1823c465d2131067348ea10b36a5a813d682ddf3f03c2177d160"
const receiverAddress = "0xbf00c30b93d76ab3d45625645b752b68199c8221"
const gasPrice = "20000000000"

func setup(t *testing.T) *exec.Cmd {
	var ganache = exec.Command(
		"/Users/walker/.npm-global/bin/ganache-cli",
		"--account", "0x"+senderKey+",111111111111111111111",
		"--account", "0x"+receiverKey+",1000000000000000000000000",
		"--account", "0x"+someGuyKey+",1000000000000000000000000",
		"-p", port, "-g", gasPrice)

	if err := ganache.Start(); err != nil {
		t.Fatal("unexpect ganache-cli exit: ", err)
	}

	//waiting for rpc service
	time.Sleep(2 * time.Second)
	return ganache
}

func makeAuth(t *testing.T, private string, client *ethclient.Client, value int64) *bind.TransactOpts {
	privateKey, err := crypto.HexToECDSA(private)
	if err != nil {
		t.Fatal(err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatal("cannot assert type: publicKey is not of type *ecdsa.PublicKey")
	}

	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)

	nonce, err := client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		t.Fatal(err)
	}

	//gasPrice, err := client.SuggestGasPrice(context.Background())
	//if err != nil {
	//	t.Fatal(err)
	//}

	auth := bind.NewKeyedTransactor(privateKey)
	auth.Nonce = big.NewInt(int64(nonce))
	//in wei
	auth.Value = big.NewInt(value)
	auth.GasLimit = uint64(3000000) //in uints

	gasPriceInt, _ := big.NewInt(0).SetString(gasPrice, 10)
	auth.GasPrice = gasPriceInt

	return auth
}

func TestNewContract(t *testing.T) {
	var ganache = setup(t)
	defer func() {
		_ = ganache.Process.Kill()
	}()

	var timeLock1Hour = time.Now().Unix() + hourSeconds
	var receiver = common.HexToAddress(receiverAddress)
	var hashPair = utils.NewSecretHashPair()

	client, err := ethclient.Dial("http://127.0.0.1:7545")
	if err != nil {
		t.Fatal(err)
	}

	senderAuth := makeAuth(t, senderKey, client, 0)

	address, _, instance, err := DeployHtlc(senderAuth, client)
	if err != nil {
		t.Fatal(err)
	}

	senderAuth = makeAuth(t, senderKey, client, oneFinney)

	newContractTx, err := instance.NewContract(senderAuth, receiver, hashPair.Hash, big.NewInt(timeLock1Hour))
	if err != nil {
		t.Fatal(err)
	}

	fmt.Println("newContract:  ", hexutil.Encode(hashPair.Hash[:]), timeLock1Hour, hashPair.Secret, address.Hash().String())
	paddedSecret := utils.LeftPad32Bytes([]byte(hashPair.Secret))
	fmt.Println("preimage:  ", hexutil.Encode(paddedSecret[:]))

	receipt, err := client.TransactionReceipt(context.Background(), newContractTx.Hash())
	if err != nil {
		t.Fatal(err)
	}

	contractABI, err := abi.JSON(strings.NewReader(string(HtlcABI)))
	if err != nil {
		t.Fatal("Fatal read contract abi", err)
	}

	var logHTLCEvent HtlcLogHTLCNew
	if err := contractABI.Unpack(&logHTLCEvent, "LogHTLCNew", receipt.Logs[0].Data); err != nil {
		t.Fatal("Fatal unpack log data for LogHTLCNew", err)
	}

	logHTLCEvent.ContractId = receipt.Logs[0].Topics[1]
	logHTLCEvent.Sender = common.HexToAddress(receipt.Logs[0].Topics[2].Hex())
	logHTLCEvent.Receiver = common.HexToAddress(receipt.Logs[0].Topics[3].Hex())
	fmt.Println(logHTLCEvent.ContractId)

	//Check logHTLCEvent
	if match, _ := regexp.MatchString("^0x[0-9a-f]{64}$", hexutil.Encode(logHTLCEvent.ContractId[:])); match == false {
		t.Fatal("logHTLCEvent.ContractId should be Sha256Hash")
	}

	if senderAuth.From.Hex() != logHTLCEvent.Sender.Hex() {
		t.Fatal("logHTLCEvent.Sender should be the specified sender")
	}

	if receiver.Hex() != logHTLCEvent.Receiver.Hex() {
		t.Fatal("logHTLCEvent.Receiver should be the specified receiver")
	}

	if big.NewInt(oneFinney).Cmp(logHTLCEvent.Amount) != 0 {
		t.Fatal("logHTLCEvent.Amount should be equal oneFinney")
	}

	if hexutil.Encode(hashPair.Hash[:]) != hexutil.Encode(logHTLCEvent.Hashlock[:]) {
		t.Fatal("logHTLCEvent.Hashlock should be the specified hashlock")
	}

	if big.NewInt(timeLock1Hour).Cmp(logHTLCEvent.Timelock) != 0 {
		t.Fatal("logHTLCEvent.Timelock should be the specified timelock")
	}

	//Check contract details on-chain
	contractDetails, err := instance.GetContract(&bind.CallOpts{From: senderAuth.From}, logHTLCEvent.ContractId)
	if err != nil {
		t.Fatal("Fatal call GetContract")
	}

	fmt.Printf("contractDetails:%v\n", contractDetails)

	if senderAuth.From.Hex() != contractDetails.Sender.Hex() {
		t.Fatal("contractDetails.Sender should be the specified sender")
	}

	if receiver.Hex() != contractDetails.Receiver.Hex() {
		t.Fatal("contractDetails.Receiver should be the specified receiver")
	}

	if big.NewInt(oneFinney).Cmp(contractDetails.Amount) != 0 {
		t.Fatal("contractDetails.Amount should be equal oneFinney")
	}

	if hexutil.Encode(hashPair.Hash[:]) != hexutil.Encode(contractDetails.Hashlock[:]) {
		t.Fatal("contractDetails.Hashlock should be the specified hashlock")
	}

	if big.NewInt(timeLock1Hour).Cmp(contractDetails.Timelock) != 0 {
		t.Fatal("contractDetails.Timelock should be the specified timelock")
	}

	if (hexutil.Encode(contractDetails.Preimage[:])) != "0x0000000000000000000000000000000000000000000000000000000000000000" {
		t.Fatal("contractDetails.Preimage string should be 0x0000000000000000000000000000000000000000000000000000000000000000")
	}

	if contractDetails.Refunded != false {
		t.Fatal("contractDetails.Refunded should be false")
	}

	if contractDetails.Withdrawn != false {
		t.Fatal("contractDetails.Refunded should be false")
	}

}

//newContract() should fail when no ETH sent
func TestNewContractWithNoETH(t *testing.T) {
	var ganache = setup(t)
	defer func() {
		_ = ganache.Process.Kill()
	}()

	var timeLock1Hour = time.Now().Unix() + hourSeconds
	var receiver = common.HexToAddress(receiverAddress)
	var hashPair = utils.NewSecretHashPair()

	client, err := ethclient.Dial("http://127.0.0.1:7545")
	if err != nil {
		t.Fatal(err)
	}

	senderAuth := makeAuth(t, senderKey, client, 0)

	_, _, instance, err := DeployHtlc(senderAuth, client)
	if err != nil {
		t.Fatal(err)
	}

	senderAuth = makeAuth(t, senderKey, client, 0)

	_, err = instance.NewContract(senderAuth, receiver, hashPair.Hash, big.NewInt(timeLock1Hour))
	if !(err != nil && strings.HasPrefix(err.Error(), REQUIRE_FAILED_MSG)) {
		t.Fatal("expected failure due to 0 value transferred")
	}
}

//newContract() should fail with timelocks in the past
func TestNewContractWithPastTimelocks(t *testing.T) {
	var ganache = setup(t)
	defer func() {
		_ = ganache.Process.Kill()
	}()

	var timeLockPast = time.Now().Unix() - 1
	var receiver = common.HexToAddress(receiverAddress)
	var hashPair = utils.NewSecretHashPair()

	client, err := ethclient.Dial("http://127.0.0.1:7545")
	if err != nil {
		t.Fatal(err)
	}

	senderAuth := makeAuth(t, senderKey, client, 0)

	_, _, instance, err := DeployHtlc(senderAuth, client)
	if err != nil {
		t.Fatal(err)
	}

	senderAuth = makeAuth(t, senderKey, client, oneFinney)

	_, err = instance.NewContract(senderAuth, receiver, hashPair.Hash, big.NewInt(timeLockPast))
	if !(err != nil && strings.HasPrefix(err.Error(), REQUIRE_FAILED_MSG)) {
		t.Fatal("expected failure due past timelock")
	}
}

//newContract() should reject a duplicate contract request
func TestNewContractDuplicate(t *testing.T) {
	var ganache = setup(t)
	defer func() {
		_ = ganache.Process.Kill()
	}()

	var timeLock1Hour = time.Now().Unix() + hourSeconds
	var receiver = common.HexToAddress(receiverAddress)
	var hashPair = utils.NewSecretHashPair()

	client, err := ethclient.Dial("http://127.0.0.1:7545")
	if err != nil {
		t.Fatal(err)
	}

	senderAuth := makeAuth(t, senderKey, client, 0)

	_, _, instance, err := DeployHtlc(senderAuth, client)
	if err != nil {
		t.Fatal(err)
	}

	senderAuth = makeAuth(t, senderKey, client, oneFinney)

	_, err = instance.NewContract(senderAuth, receiver, hashPair.Hash, big.NewInt(timeLock1Hour))
	if err != nil {
		t.Fatal(err)
	}

	_, err = instance.NewContract(senderAuth, receiver, hashPair.Hash, big.NewInt(timeLock1Hour))
	if !(err != nil && strings.HasPrefix(err.Error(), REQUIRE_FAILED_TX_MSG)) {
		t.Log(err)
		t.Fatal("expected failure due to duplicate request")
	}
}

//withdraw() should send receiver funds when given the correct secret preimage
func TestWithdraw(t *testing.T) {
	var ganache = setup(t)
	defer func() {
		_ = ganache.Process.Kill()
	}()

	var timeLock1Hour = time.Now().Unix() + hourSeconds
	var receiver = common.HexToAddress(receiverAddress)
	var hashPair = utils.NewSecretHashPair()

	client, err := ethclient.Dial("http://127.0.0.1:7545")
	if err != nil {
		t.Fatal(err)
	}

	senderAuth := makeAuth(t, senderKey, client, 0)

	_, _, instance, err := DeployHtlc(senderAuth, client)
	if err != nil {
		t.Fatal(err)
	}

	senderAuth = makeAuth(t, senderKey, client, oneFinney)

	newContractTx, err := instance.NewContract(senderAuth, receiver, hashPair.Hash, big.NewInt(timeLock1Hour))
	if err != nil {
		t.Fatal(err)
	}

	newContractTxReceipt, err := client.TransactionReceipt(context.Background(), newContractTx.Hash())
	if err != nil {
		t.Fatal(err)
	}

	contractId := newContractTxReceipt.Logs[0].Topics[1]
	receiverBalBefore, err := client.BalanceAt(context.Background(), receiver, nil)
	if err != nil {
		t.Fatal("Fatal get receiver balance", err)
	}

	receiverAuth := makeAuth(t, receiverKey, client, 0)
	paddedSecret := utils.LeftPad32Bytes([]byte(hashPair.Secret))

	fmt.Println(hashPair.Secret, paddedSecret)

	withdrawTx, err := instance.Withdraw(receiverAuth, contractId, paddedSecret)
	if err != nil {
		t.Fatal("Fatal withdraw with the specified contractId and secret", err)
	}

	withdrawTxReceipt, err := client.TransactionReceipt(context.Background(), withdrawTx.Hash())
	gasPriceInt, _ := big.NewInt(0).SetString(gasPrice, 10)
	txGas := big.NewInt(0).Mul(big.NewInt(0).SetUint64(withdrawTxReceipt.GasUsed), gasPriceInt)

	receiverBalAfter, err := client.BalanceAt(context.Background(), receiver, nil)
	if err != nil {
		t.Fatal("Fatal get receiver balance", err)
	}

	expectedBal := big.NewInt(0).Sub(big.NewInt(0).Add(receiverBalBefore, big.NewInt(oneFinney)), txGas)
	if receiverBalAfter.Cmp(expectedBal) != 0 {
		t.Fatal("receiver balance doesn't match")
	}

	contractDetails, err := instance.GetContract(&bind.CallOpts{From: receiver}, contractId)
	if err != nil {
		t.Fatal("Fatal GetContract call")
	}

	if contractDetails.Withdrawn != true {
		t.Fatal("GetContract Withdrawn should be true")
	}

	if contractDetails.Refunded != false {
		t.Fatal("GetContract Refunded should be true")
	}

	if string(contractDetails.Preimage[:]) != string(paddedSecret[:]) {
		t.Fatal("GetContract Preimage doesn't match")
	}
}

//withdraw() should fail if preimage does not hash to hashX
func TestWithdrawMismatchPreimage(t *testing.T) {
	var ganache = setup(t)
	defer func() {
		_ = ganache.Process.Kill()
	}()

	var timeLock1Hour = time.Now().Unix() + hourSeconds
	var receiver = common.HexToAddress(receiverAddress)
	var hashPair = utils.NewSecretHashPair()

	client, err := ethclient.Dial("http://127.0.0.1:7545")
	if err != nil {
		t.Fatal(err)
	}

	senderAuth := makeAuth(t, senderKey, client, 0)

	_, _, instance, err := DeployHtlc(senderAuth, client)
	if err != nil {
		t.Fatal(err)
	}

	senderAuth = makeAuth(t, senderKey, client, oneFinney)

	newContractTx, err := instance.NewContract(senderAuth, receiver, hashPair.Hash, big.NewInt(timeLock1Hour))
	if err != nil {
		t.Fatal(err)
	}

	newContractTxReceipt, err := client.TransactionReceipt(context.Background(), newContractTx.Hash())
	if err != nil {
		t.Fatal(err)
	}

	contractId := newContractTxReceipt.Logs[0].Topics[1]

	receiverAuth := makeAuth(t, receiverKey, client, 0)
	wrongSecret := utils.LeftPad32Bytes([]byte("random"))
	_, err = instance.Withdraw(receiverAuth, contractId, wrongSecret)
	if !(err != nil && strings.HasPrefix(err.Error(), REQUIRE_FAILED_MSG)) {
		t.Fatal("expected failure due to mismatch preimage")
	}

}

//withdraw() should fail if caller is not the receiver
func TestWithdrawNotReceiver(t *testing.T) {
	var ganache = setup(t)
	defer func() {
		_ = ganache.Process.Kill()
	}()

	var timeLock1Hour = time.Now().Unix() + hourSeconds
	var receiver = common.HexToAddress(receiverAddress)
	var hashPair = utils.NewSecretHashPair()

	client, err := ethclient.Dial("http://127.0.0.1:7545")
	if err != nil {
		t.Fatal(err)
	}

	senderAuth := makeAuth(t, senderKey, client, 0)

	_, _, instance, err := DeployHtlc(senderAuth, client)
	if err != nil {
		t.Fatal(err)
	}

	senderAuth = makeAuth(t, senderKey, client, oneFinney)

	newContractTx, err := instance.NewContract(senderAuth, receiver, hashPair.Hash, big.NewInt(timeLock1Hour))
	if err != nil {
		t.Fatal(err)
	}

	newContractTxReceipt, err := client.TransactionReceipt(context.Background(), newContractTx.Hash())
	if err != nil {
		t.Fatal(err)
	}

	contractId := newContractTxReceipt.Logs[0].Topics[1]

	someGuyAuth := makeAuth(t, someGuyKey, client, 0)
	paddedSecret := utils.LeftPad32Bytes([]byte(hashPair.Secret))
	_, err = instance.Withdraw(someGuyAuth, contractId, paddedSecret)
	if !(err != nil && strings.HasPrefix(err.Error(), REQUIRE_FAILED_MSG)) {
		t.Fatal("expected failure due to not correct receiver")
	}
}

//withdraw() should fail after timelock expiry
func TestWithdrawAfterTimelock(t *testing.T) {
	var ganache = setup(t)
	defer func() {
		_ = ganache.Process.Kill()
	}()

	var timeLock1Second = time.Now().Unix() + 1
	var receiver = common.HexToAddress(receiverAddress)
	var hashPair = utils.NewSecretHashPair()

	client, err := ethclient.Dial("http://127.0.0.1:7545")
	if err != nil {
		t.Fatal(err)
	}

	senderAuth := makeAuth(t, senderKey, client, 0)

	_, _, instance, err := DeployHtlc(senderAuth, client)
	if err != nil {
		t.Fatal(err)
	}

	senderAuth = makeAuth(t, senderKey, client, oneFinney)

	newContractTx, err := instance.NewContract(senderAuth, receiver, hashPair.Hash, big.NewInt(timeLock1Second))
	if err != nil {
		t.Fatal(err)
	}

	newContractTxReceipt, err := client.TransactionReceipt(context.Background(), newContractTx.Hash())
	if err != nil {
		t.Fatal(err)
	}

	contractId := newContractTxReceipt.Logs[0].Topics[1]

	time.Sleep(time.Second)

	receiverAuth := makeAuth(t, receiverKey, client, 0)
	paddedSecret := utils.LeftPad32Bytes([]byte(hashPair.Secret))
	_, err = instance.Withdraw(receiverAuth, contractId, paddedSecret)
	if !(err != nil && strings.HasPrefix(err.Error(), REQUIRE_FAILED_MSG)) {
		t.Fatal("expected failure due to not correct receiver")
	}
}

//refund() should pass after timelock expiry
func TestRefund(t *testing.T) {
	var ganache = setup(t)
	defer func() {
		_ = ganache.Process.Kill()
	}()

	var timeLock1Second = time.Now().Unix() + 1
	var receiver = common.HexToAddress(receiverAddress)
	var hashPair = utils.NewSecretHashPair()

	client, err := ethclient.Dial("http://127.0.0.1:7545")
	if err != nil {
		t.Fatal(err)
	}

	senderAuth := makeAuth(t, senderKey, client, 0)

	_, _, instance, err := DeployHtlc(senderAuth, client)
	if err != nil {
		t.Fatal(err)
	}

	senderAuth = makeAuth(t, senderKey, client, oneFinney)

	newContractTx, err := instance.NewContract(senderAuth, receiver, hashPair.Hash, big.NewInt(timeLock1Second))
	if err != nil {
		t.Fatal(err)
	}

	newContractTxReceipt, err := client.TransactionReceipt(context.Background(), newContractTx.Hash())
	if err != nil {
		t.Fatal(err)
	}

	contractId := newContractTxReceipt.Logs[0].Topics[1]

	time.Sleep(time.Second)

	senderBalBefore, err := client.BalanceAt(context.Background(), senderAuth.From, nil)
	if err != nil {
		t.Fatal("Fatal get sender balance", err)
	}

	senderAuth = makeAuth(t, senderKey, client, 0)
	refundTx, err := instance.Refund(senderAuth, contractId)
	if err != nil {
		t.Fatal(err)
	}

	refundTxReceipt, err := client.TransactionReceipt(context.Background(), refundTx.Hash())
	if err != nil {
		t.Fatal(err)
	}
	gasPriceInt, _ := big.NewInt(0).SetString(gasPrice, 10)
	txGas := big.NewInt(0).Mul(big.NewInt(0).SetUint64(refundTxReceipt.GasUsed), gasPriceInt)
	expectedBal := senderBalBefore.Sub(big.NewInt(0).Add(senderBalBefore, big.NewInt(oneFinney)), txGas)

	senderBalAfter, err := client.BalanceAt(context.Background(), senderAuth.From, nil)
	if err != nil {
		t.Fatal("Fatal get sender balance", err)
	}
	if senderBalAfter.Cmp(expectedBal) != 0 {
		t.Fatal("sender balance doesn't match")
	}

	contractDetails, err := instance.GetContract(&bind.CallOpts{From: senderAuth.From}, contractId)
	if err != nil {
		t.Fatal("Fatal GetContract call")
	}

	if !contractDetails.Refunded {
		t.Fatal("GetContract Refunded should be true")
	}

	if contractDetails.Withdrawn {
		t.Fatal("GetContract Refunded should be false")
	}
}

//refund() should fail before the timelock expiry
func TestRefundBeforeTimelock(t *testing.T) {
	var ganache = setup(t)
	defer func() {
		_ = ganache.Process.Kill()
	}()

	var timeLock1Hour = time.Now().Unix() + hourSeconds
	var receiver = common.HexToAddress(receiverAddress)
	var hashPair = utils.NewSecretHashPair()

	client, err := ethclient.Dial("http://127.0.0.1:7545")
	if err != nil {
		t.Fatal(err)
	}

	senderAuth := makeAuth(t, senderKey, client, 0)

	_, _, instance, err := DeployHtlc(senderAuth, client)
	if err != nil {
		t.Fatal(err)
	}

	senderAuth = makeAuth(t, senderKey, client, oneFinney)

	newContractTx, err := instance.NewContract(senderAuth, receiver, hashPair.Hash, big.NewInt(timeLock1Hour))
	if err != nil {
		t.Fatal(err)
	}

	newContractTxReceipt, err := client.TransactionReceipt(context.Background(), newContractTx.Hash())
	if err != nil {
		t.Fatal(err)
	}

	contractId := newContractTxReceipt.Logs[0].Topics[1]

	senderAuth = makeAuth(t, senderKey, client, 0)
	_, err = instance.Refund(senderAuth, contractId)
	if !(err != nil && strings.HasPrefix(err.Error(), REQUIRE_FAILED_MSG)) {
		t.Fatal("expected failure due to timelock active")
	}
}

//getContract() returns empty record when contract doesn't exist
func TestGetContract(t *testing.T) {
	var ganache = setup(t)
	defer func() {
		_ = ganache.Process.Kill()
	}()

	client, err := ethclient.Dial("http://127.0.0.1:7545")
	if err != nil {
		t.Fatal(err)
	}

	senderAuth := makeAuth(t, senderKey, client, 0)

	_, _, instance, err := DeployHtlc(senderAuth, client)
	if err != nil {
		t.Fatal(err)
	}

	contractDetails, err := instance.GetContract(&bind.CallOpts{From: senderAuth.From}, [32]byte{0xab, 0xcd, 0xef})
	if err != nil {
		t.Fatal("Fatal GetContract call")
	}

	if contractDetails.Sender.String() != "0x0000000000000000000000000000000000000000" {
		t.Fatal("GetContract Sender should be empty")
	}
}

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"flag"
	"fmt"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/walker1992/atomicswap/contract"
	"github.com/walker1992/atomicswap/log"
	"math/big"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	flagset     = flag.NewFlagSet("", flag.ExitOnError)
	connectFlag = flagset.String("server", "http://127.0.0.1:8545", "host[:port] of eth RPC server")
)

//0xCdce1E6B706e7e4Ce269Ba1D4aaAED2fdc78690a
const senderKey = "b80dbf638b9128e54f3222d2b6d3213d45521d49bb6317abdf34b219a55204b7"

//0xbF00C30b93d76ab3D45625645b752b68199c8221
const receiverKey = "08cd4fde21e980c7d05afa3b0d4d27534e646be3cc3a67b303b055d1166cbae3"

func init() {
	flagset.Usage = func() {
		fmt.Println("Usage: ethatomicswap [flags] cmd [cmd args]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  deloyContract: <participant address> <amount>")
		fmt.Println("  newContract: <initiator address> <amount> <secret hash>")
		fmt.Println("  withdraw: <contractAddress> <contractId> <secret>")
		fmt.Println("  refund: <contractAddress> <contractId>")
		fmt.Println("  getContract: <contractAddress> <contractId> ")

		fmt.Println()
		fmt.Println("Flags:")
		flagset.PrintDefaults()
	}
}

func main() {
	err, showUsage := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if showUsage {
		flagset.Usage()
	}
	if err != nil || showUsage {
		os.Exit(1)
	}
}

func run() (err error, showUsage bool) {
	flagset.Parse(os.Args[1:])
	args := flagset.Args()
	if len(args) == 0 {
		return nil, true
	}
	cmdArgs := 0
	switch args[0] {
	case "deployContract":
		cmdArgs = 0
		log.Info("deployContract")
	case "newContract":
		cmdArgs = 5
		log.Info("newContract")
	case "withdraw":
		cmdArgs = 3
		log.Info("withdraw")
	case "refund":
		cmdArgs = 1
		log.Info("refund")
	case "getContract":
		cmdArgs = 2
		log.Info("getContract")
	default:
		return fmt.Errorf("Unkonw command %v\n", os.Args[1]), true
	}

	nArgs := checkCmdArgLength(args[1:], cmdArgs)
	flagset.Parse(args[1+nArgs:])
	if nArgs < cmdArgs {
		return fmt.Errorf("%s too few arguments", args[0]), true
	}
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected argment: %s", flagset.Arg(0)), true
	}

	client, err := ethclient.Dial(*connectFlag)
	if err != nil {
		return fmt.Errorf("failed to connect server: %s", err), true
	}

	switch args[0] {
	case "deployContract":
		log.Info("deployContract")
		address, tx, err := deployContract(client)
		if err != nil {
			return fmt.Errorf("failed to deploy contract: %s", err), true
		}
		fmt.Printf("contract address = %s\n", address.Hex())
		fmt.Printf("transaction hash = %s\n", tx.Hash().Hex())
	case "newContract":
		contractAddr := args[1]
		receiver := common.HexToAddress(args[2])
		amount, err := strconv.ParseInt(args[3], 10, 64)
		if err != nil {
			return err, true
		}
		timelock, err := strconv.ParseInt(args[4], 10, 64)
		if err != nil {
			return err, true
		}
		secret := NewSecret(args[5])
		fmt.Printf("\n please remember the secret:\n secret:     %s\n secretHash: %s\n\n", secret.Secret, hexutil.Encode(secret.Hash[:]))

		contract, err := newContract(client, contractAddr, receiver, amount, timelock, secret.Hash)
		if err != nil {
			return err, true
		}

		fmt.Printf("ContractAddr = %s\n", contractAddr)
		fmt.Printf("ContractId = %s\n", hexutil.Encode(contract.ContractId[:]))
		fmt.Printf("Sender     = %s\n", contract.Sender.String())
		fmt.Printf("Receiver   = %s\n", contract.Receiver.String())
		fmt.Printf("Amount     = %s\n", contract.Amount)
		fmt.Printf("TimeLock   = %s\n", contract.Timelock)
		fmt.Printf("SecretHash = %s\n", hexutil.Encode(contract.Hashlock[:]))

	case "withdraw":
		log.Info("withdraw")
		err := withdraw(client, args[1], args[2], args[3])
		if err != nil {
			return err, true
		}
		fmt.Println("Withdraw the contract ", args[2])

	case "refund":
		err := refund(client, args[1], args[2])
		if err != nil {
			return err, true
		}
		fmt.Println("Refunded the contract: ", args[2])

	case "getContract":
		contract, err := getContract(client, args[1], args[2])
		if err != nil {
			return err, true
		}
		fmt.Printf("Sender     = %s\n", contract.Sender.String())
		fmt.Printf("Receiver   = %s\n", contract.Receiver.String())
		fmt.Printf("Amount     = %s (wei)\n", contract.Amount)
		fmt.Printf("TimeLock   = %s (%s)\n", contract.Timelock, time.Unix(contract.Timelock.Int64(), 0))
		fmt.Printf("SecretHash = %s\n", hexutil.Encode(contract.Hashlock[:]))
		fmt.Printf("Withdrawn  = %v\n", contract.Withdrawn)
		fmt.Printf("Refunded   = %v\n", contract.Refunded)
		fmt.Printf("Secret     = %s\n", hexutil.Encode(contract.Preimage[:]))
	}
	return nil, false
}

func checkCmdArgLength(args []string, required int) (Args int) {
	if len(args) < required {
		return 0
	}
	for i, arg := range args[:required] {
		if len(arg) != 1 && strings.HasPrefix(arg, "-") {
			return i
		}
	}
	return required
}

func deployContract(client *ethclient.Client) (contractAddr common.Address, trx *types.Transaction, err error) {
	auth := makeAuth(senderKey, client, 0)

	log.Info("Deploy contract...")

	address, tx, _, err := htlc.DeployHtlc(auth, client)
	if err != nil {
		return common.Address{}, nil, err
	}

	log.Info("contract address = %s", address.Hex())
	log.Info("transaction hash = %s", tx.Hash().Hex())
	return address, tx, nil
}

func loadHTLC(client *ethclient.Client, contractAddress common.Address) *htlc.Htlc {
	instance, err := htlc.NewHtlc(contractAddress, client)
	if err != nil {
		log.Crit("Failed to load contract: %s", err)
	}

	log.Info("contract at %s is loaded", contractAddress.String())
	return instance
}

func newContract(client *ethclient.Client, contractAddr string, receiver common.Address, _amount, _timelock int64, _hashlock [32]byte) (contract *htlc.HtlcLogHTLCNew, err error) {
	if IsValidAddress(contractAddr) == false {
		return nil, fmt.Errorf("invalid address: %s", contractAddr)
	}
	instance := loadHTLC(client, common.HexToAddress(contractAddr))

	timeLock := time.Now().Unix() + _timelock
	senderAuth := makeAuth(senderKey, client, _amount)

	newContractTx, err := instance.NewContract(senderAuth, receiver, _hashlock, big.NewInt(timeLock))
	if err != nil {
		return nil, fmt.Errorf("failed to initiate contract %s", err)
	}

	log.Info("NewContract trx hash = %s", newContractTx.Hash().String())

	time.Sleep(30 * time.Second)
	receipt, err := client.TransactionReceipt(context.Background(), newContractTx.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get tx %s receipt %s", newContractTx.Hash(), err)
	}

	if receipt.Status != 1 {
		return nil, fmt.Errorf("failed to NewContract,tx %s", newContractTx.Hash().String())
	}
	contractABI, err := abi.JSON(strings.NewReader(string(htlc.HtlcABI)))
	if err != nil {
		return nil, fmt.Errorf("failed to read contract abi %s", err)
	}

	var logHTLCEvent htlc.HtlcLogHTLCNew
	if err := contractABI.Unpack(&logHTLCEvent, "LogHTLCNew", receipt.Logs[0].Data); err != nil {
		return nil, fmt.Errorf("failed to unpack log data for LogHTLCNew %s", err)
	}

	logHTLCEvent.ContractId = receipt.Logs[0].Topics[1]
	logHTLCEvent.Sender = common.HexToAddress(receipt.Logs[0].Topics[2].Hex())
	logHTLCEvent.Receiver = common.HexToAddress(receipt.Logs[0].Topics[3].Hex())

	return &logHTLCEvent, nil
}

func withdraw(client *ethclient.Client, contractAddr string, contractId string, secret string) error {
	if IsValidAddress(contractAddr) == false {
		return fmt.Errorf("invalid address: %s", contractAddr)
	}
	instance := loadHTLC(client, common.HexToAddress(contractAddr))

	receiverAuth := makeAuth(receiverKey, client, 0)

	WithdrawTx, err := instance.Withdraw(receiverAuth, common.HexToHash(contractId), NewSecret(secret).SeedBytes())
	if err != nil {
		return fmt.Errorf("failed to withdraw with the specified contractId and secret: %s", err)
	}
	fmt.Printf("transaction hash = %s\n", WithdrawTx.Hash().String())

	time.Sleep(30 * time.Second)
	receipt, err := client.TransactionReceipt(context.Background(), WithdrawTx.Hash())
	if err != nil {
		return fmt.Errorf("failed to get tx %s receipt err:%s", WithdrawTx.Hash().String(), err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("failed to Execute withdraw trx %s", WithdrawTx.Hash().String())
	}

	balance, _ := client.BalanceAt(context.Background(), receiverAuth.From, nil)
	fmt.Printf("Your balance now is %s\n", balance.String())
	return nil
}

type Contract struct {
	Sender   common.Address
	Receiver common.Address
	Amount   *big.Int
	Hashlock [32]byte

	Timelock  *big.Int
	Withdrawn bool
	Refunded  bool
	Preimage  [32]byte

	ContractId string
}

func getContract(client *ethclient.Client, contractAddr, contractId string) (Contract, error) {
	if IsValidAddress(contractAddr) == false {
		return Contract{}, fmt.Errorf("invalid address: %s", contractAddr)
	}

	instance := loadHTLC(client, common.HexToAddress(contractAddr))

	senderAuth := makeAuth(senderKey, client, 0)

	contractDetails, err := instance.GetContract(&bind.CallOpts{From: senderAuth.From}, common.HexToHash(contractId))
	if err != nil {
		return Contract{}, fmt.Errorf("failed to GetContract call")
	}

	return Contract{
		Sender:    contractDetails.Sender,
		Receiver:  contractDetails.Receiver,
		Amount:    contractDetails.Amount,
		Timelock:  contractDetails.Timelock,
		Hashlock:  contractDetails.Hashlock,
		Withdrawn: contractDetails.Withdrawn,
		Refunded:  contractDetails.Refunded,
		Preimage:  contractDetails.Preimage,
	}, nil
}

func refund(client *ethclient.Client, contractAddr, contractId string) error {
	if IsValidAddress(contractAddr) == false {
		return fmt.Errorf("invalid address: %s", contractAddr)
	}

	senderAuth := makeAuth(senderKey, client, 0)
	instance := loadHTLC(client, common.HexToAddress(contractAddr))
	refundTx, err := instance.Refund(senderAuth, common.HexToHash(contractId))
	if err != nil {
		return fmt.Errorf("failed to refund the contract: %s, %s", contractId, err)
	}

	fmt.Printf("Transaction hash = %s\n", refundTx.Hash().String())

	time.Sleep(30 * time.Second)
	receipt, err := client.TransactionReceipt(context.Background(), refundTx.Hash())
	if err != nil {
		return fmt.Errorf("failed to get tx %s receipt", refundTx.Hash().String())
	}
	if receipt.Status != 1 {
		return fmt.Errorf("failed to execute refund tx %s", refundTx.Hash().String())
	}

	return nil
}

// IsValidAddress validate hex address
func IsValidAddress(address string) bool {
	return regexp.MustCompile("^0x[0-9a-fA-F]{40}$").MatchString(address)
}

func makeAuth(private string, client *ethclient.Client, value int64) *bind.TransactOpts {
	privateKey, err := crypto.HexToECDSA(private)
	if err != nil {
		log.Crit("Failed to parse private key: %s", err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		log.Crit("cannot assert type: publicKey is not of type *ecdsa.PublicKey")
	}

	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)

	nonce, err := client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		log.Crit("Failed to get nonce: %s", err)
	}

	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		log.Crit("Failed to get gasPrice: %s", err)
	}

	auth := bind.NewKeyedTransactor(privateKey)
	auth.Nonce = big.NewInt(int64(nonce))
	auth.Value = big.NewInt(value)  //in wei
	auth.GasLimit = uint64(3000000) //in uints

	gasPriceInt, _ := big.NewInt(0).SetString(gasPrice.String(), 10)
	auth.GasPrice = gasPriceInt

	return auth
}

type SecretHashPair struct {
	Secret string
	Hash   [32]byte
}

// LeftPad32Bytes zero-pads slice to the left up to length 32.
func LeftPad32Bytes(slice []byte) [32]byte {
	var padded [32]byte
	if 32 <= len(slice) {
		copy(padded[:], slice[:32])
	} else {
		copy(padded[32-len(slice):], slice)
	}
	return padded
}

func NewSecret(seed string) *SecretHashPair {
	secret := LeftPad32Bytes([]byte(seed))
	return &SecretHashPair{
		Secret: string(secret[:]),
		Hash:   sha256.Sum256(secret[:]),
	}
}
func (s *SecretHashPair) SeedBytes() (seed [32]byte) {
	in := []byte(s.Secret)
	if 32 <= len(in) {
		copy(seed[:], in[:32])
	} else {
		copy(seed[32-len(in):], in)
	}
	return seed
}

package main

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/walker1992/atomicswap/contract"
	"github.com/walker1992/atomicswap/log"
	"github.com/walker1992/atomicswap/utils"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

//0xCdce1E6B706e7e4Ce269Ba1D4aaAED2fdc78690a
const senderKey = "b80dbf638b9128e54f3222d2b6d3213d45521d49bb6317abdf34b219a55204b7"

//0xbF00C30b93d76ab3D45625645b752b68199c8221
const receiverKey = "08cd4fde21e980c7d05afa3b0d4d27534e646be3cc3a67b303b055d1166cbae3"

const (
	TwoDay  = 2 * 24 * 60 * 60
	OneDay  = 24 * 60 * 60
	OneHour = 60 * 60
)

type Contract struct {
	Sender    common.Address
	Receiver  common.Address
	Amount    *big.Int
	Hashlock  [32]byte
	Timelock  *big.Int
	Withdrawn bool
	Refunded  bool
	Preimage  [32]byte
}

func main() {
	client, err := ethclient.Dial("http://127.0.0.1:8545")
	if err != nil {
		log.Crit("Failed to connect server: %s", err)
	}

	address, trx, err := deployContract(client)
	if err != nil {
		log.Crit("Failed to deploy contract: %s", err)
	}

	loadHTLC(client, address)
	log.Info(trx.Hash().String())

	var hashPair = utils.NewSecretHashPair()
	fmt.Printf("\n please remember the secret:\n secret:     %s\n secretHash: %s\n\n", hashPair.Secret, hexutil.Encode(hashPair.Hash[:]))

	contractId := initiateContract(client, address.String(), common.HexToAddress("0xbF00C30b93d76ab3D45625645b752b68199c8221"), 15000000000, TwoDay, hashPair.Hash)

	//getContract(client, address.String(), contractId)
	//contract :=GetContract(client, address.String(), contractId)
	//fmt.Print("%v",contract)
	//if !withdraw(client, address.String(), contractId, hashPair.Secret) {
	//	log.Error("Failed to Execute Withdraw !!")
	//}

	getContract(client, address.String(), contractId)

	//if !refund(client, address.String(), contractId) {
	//	log.Error("Failed to Execute refund!!")
	//}

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

func initiateContract(client *ethclient.Client, contractAddr string, receiver common.Address, _amount, _timelock int64, _hashlock [32]byte) (contractId string) {
	log.Info("Initiate contract...")
	if IsValidAddress(contractAddr) == false {
		log.Crit("Invalid address: %s", contractAddr)
	}
	instance := loadHTLC(client, common.HexToAddress(contractAddr))

	timeLock := time.Now().Unix() + _timelock
	senderAuth := makeAuth(senderKey, client, _amount)

	newContractTx, err := instance.NewContract(senderAuth, receiver, _hashlock, big.NewInt(timeLock))
	if err != nil {
		log.Crit("Failed to initiate contract", err)
	}

	log.Info("NewContract trx hash = %s", newContractTx.Hash().String())

	time.Sleep(30 * time.Second)
	receipt, err := client.TransactionReceipt(context.Background(), newContractTx.Hash())
	if err != nil {
		log.Crit("Failed to get tx %s receipt", newContractTx.Hash(), err)
	}

	if receipt.Status != 1 {
		log.Error("Failed to NewContract,tx %s", newContractTx.Hash().String())
		return
	}
	contractABI, err := abi.JSON(strings.NewReader(string(htlc.HtlcABI)))
	if err != nil {
		log.Crit("Failed to read contract abi", err)
	}

	var logHTLCEvent htlc.HtlcLogHTLCNew
	if err := contractABI.Unpack(&logHTLCEvent, "LogHTLCNew", receipt.Logs[0].Data); err != nil {
		log.Crit("Failed to unpack log data for LogHTLCNew", err)
	}

	logHTLCEvent.ContractId = receipt.Logs[0].Topics[1]
	logHTLCEvent.Sender = common.HexToAddress(receipt.Logs[0].Topics[2].Hex())
	logHTLCEvent.Receiver = common.HexToAddress(receipt.Logs[0].Topics[3].Hex())

	contractId = hexutil.Encode(logHTLCEvent.ContractId[:])

	log.Warn("ContractId = %s", hexutil.Encode(logHTLCEvent.ContractId[:]))
	log.Info("Sender     = %s", logHTLCEvent.Sender.String())
	log.Info("Receiver   = %s", logHTLCEvent.Receiver.String())
	log.Info("Amount     = %s", logHTLCEvent.Amount)
	log.Info("TimeLock   = %s", logHTLCEvent.Timelock)
	log.Info("SecretHash = %s", hexutil.Encode(logHTLCEvent.Hashlock[:]))

	return
}

func getContract(client *ethclient.Client, contractAddr, contractId string) {
	log.Info("Get contract details from %s ...", contractId)
	if IsValidAddress(contractAddr) == false {
		log.Crit("Invalid address: %s", contractAddr)
	}
	instance := loadHTLC(client, common.HexToAddress(contractAddr))

	senderAuth := makeAuth(senderKey, client, 0)

	contractDetails, err := instance.GetContract(&bind.CallOpts{From: senderAuth.From}, common.HexToHash(contractId))
	if err != nil {
		log.Crit("Failed to GetContract call")
	}

	log.Info("Sender     = %s", contractDetails.Sender.String())
	log.Info("Receiver   = %s", contractDetails.Receiver.String())
	log.Info("Amount     = %s (wei)", contractDetails.Amount)
	log.Info("TimeLock   = %s (%s)", contractDetails.Timelock, time.Unix(contractDetails.Timelock.Int64(), 0))
	log.Info("SecretHash = %s", hexutil.Encode(contractDetails.Hashlock[:]))
	log.Info("Withdrawn  = %v", contractDetails.Withdrawn)
	log.Info("Refunded   = %v", contractDetails.Refunded)
	log.Info("Secret     = %s", hexutil.Encode(contractDetails.Preimage[:]))
}

func withdraw(client *ethclient.Client, contractAddr string, contractId string, secret string) (execute bool) {
	log.Info("Withdraw from contract %s ...", contractId)
	if IsValidAddress(contractAddr) == false {
		log.Crit("Invalid address: %s", contractAddr)
	}
	instance := loadHTLC(client, common.HexToAddress(contractAddr))

	receiverAuth := makeAuth(receiverKey, client, 0)

	WithdrawTx, err := instance.Withdraw(receiverAuth, common.HexToHash(contractId), utils.LeftPad32Bytes([]byte(secret)))
	if err != nil {
		log.Crit("Failed to withdraw with the specified contractId and secret: %s", err)
	}
	log.Info("transaction hash = %s", WithdrawTx.Hash().String())

	time.Sleep(30 * time.Second)
	receipt, err := client.TransactionReceipt(context.Background(), WithdrawTx.Hash())
	if err != nil {
		log.Crit("Failed to get tx %s receipt", WithdrawTx.Hash().String(), err)
	}
	if receipt.Status != 1 {
		log.Error("Failed to Execute withdraw trx %s", WithdrawTx.Hash().String())
		return false
	}
	receiverBalAfter, err := client.BalanceAt(context.Background(), receiverAuth.From, nil)
	if err != nil {
		log.Crit("Failed to get receiver balance: %s", err)
	}
	log.Info("After withdraw balance of %s: %s", receiverAuth.From.String(), receiverBalAfter.String())

	return true
}

func refund(client *ethclient.Client, contractAddr, contractId string) (execute bool) {
	log.Info("Refund from contract %s", contractId)
	if IsValidAddress(contractAddr) == false {
		log.Crit("Invalid address: %s", contractAddr)
	}

	senderAuth := makeAuth(senderKey, client, 0)
	instance := loadHTLC(client, common.HexToAddress(contractAddr))
	refundTx, err := instance.Refund(senderAuth, common.HexToHash(contractId))
	if err != nil {
		log.Error("Failed to refund the contract: %s, %s", contractId, err)
		return false
	}

	log.Info("Transaction hash = %s", refundTx.Hash().String())

	time.Sleep(30 * time.Second)
	receipt, err := client.TransactionReceipt(context.Background(), refundTx.Hash())
	if err != nil {
		log.Error("Failed to get tx %s receipt", refundTx.Hash().String())
		return false
	}
	if receipt.Status != 1 {
		log.Error("Failed to execute refund tx %s", refundTx.Hash().String())
		return false
	}

	getContract(client, contractAddr, contractId)
	return true
}

func transfer() {
	client, err := ethclient.Dial("http://127.0.0.1:7545")
	if err != nil {
		log.Crit(err.Error())
	}

	privateKey, err := crypto.HexToECDSA(senderKey)
	if err != nil {
		log.Crit(err.Error())
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		log.Crit("cannot assert type: publicKey is not of type *ecdsa.PublicKey")
	}

	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)
	nonce, err := client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		log.Crit(err.Error())
	}

	value := big.NewInt(1000000000000000000) // in wei (1 eth)
	gasLimit := uint64(21000)                // in units
	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		log.Crit(err.Error())
	}

	toAddress := common.HexToAddress("0xbF00C30b93d76ab3D45625645b752b68199c8221")
	var data []byte
	tx := types.NewTransaction(nonce, toAddress, value, gasLimit, gasPrice, data)

	chainID, err := client.NetworkID(context.Background())
	if err != nil {
		log.Crit(err.Error())
	}

	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
	if err != nil {
		log.Crit(err.Error())
	}

	err = client.SendTransaction(context.Background(), signedTx)
	if err != nil {
		log.Crit(err.Error())
	}

	fmt.Printf("tx sent: %s", signedTx.Hash().Hex())
}

func sign(client *ethclient.Client) {
	//client.SendTransaction()

}

func GetContract(client *ethclient.Client, contractAddr, contractId string) Contract {
	log.Info("Get contract details from %s ...", contractId)
	if IsValidAddress(contractAddr) == false {
		log.Crit("Invalid address: %s", contractAddr)
	}
	instance := loadHTLC(client, common.HexToAddress(contractAddr))

	senderAuth := makeAuth(senderKey, client, 0)

	contractDetails, err := instance.GetContract(&bind.CallOpts{From: senderAuth.From}, common.HexToHash(contractId))
	if err != nil {
		log.Crit("Failed to GetContract call")
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
	}

}

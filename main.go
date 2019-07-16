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

func main() {
	client, err := ethclient.Dial("http://127.0.0.1:8545")
	if err != nil {
		log.Crit("Failed to connect server: %s", err)
	}

	deployContract(client)

	loadContract(client, common.HexToAddress("0x451d4d9309c404A31960392977e71079e5B4834f"))

	//initiateContract(client, "0x451d4d9309c404A31960392977e71079e5B4834f")
	getContract(client, "0x451d4d9309c404A31960392977e71079e5B4834f", "0xd85c34830b2afb1d405b91fb6857beb944a2d150564a5443d464180df918d3d5")

	withdraw(client,
		"0x451d4d9309c404A31960392977e71079e5B4834f",
		"0xd85c34830b2afb1d405b91fb6857beb944a2d150564a5443d464180df918d3d5",
		"0x52fdfc072182654f163f5f0f9a621d729566c74d10037c4d7bbb0407d1e2c649")

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

func deployContract(client *ethclient.Client) {
	auth := makeAuth(senderKey, client, 0)

	//Deploy contract
	log.Info("Deploy contract...")

	address, tx, _, err := htlc.DeployHtlc(auth, client)
	if err != nil {
		log.Crit("Failed to deploy contract: %s", err)
	}

	log.Info("contract address = %s", address.Hex())
	log.Info("transaction hash = %s", tx.Hash().Hex())
}

func loadContract(client *ethclient.Client, contractAddress common.Address) *htlc.Htlc {
	instance, err := htlc.NewHtlc(contractAddress, client)
	if err != nil {
		log.Crit("Failed to load contract: %s", err)
	}

	log.Info("contract at %s is loaded", contractAddress.String())
	return instance
}

func initiateContract(client *ethclient.Client, contractAddress string) {
	log.Info("Initiate contract...")
	if IsValidAddress(contractAddress) == false {
		log.Crit("Invalid address: %s", contractAddress)
	}
	instance := loadContract(client, common.HexToAddress(contractAddress))

	var timeLock1Hour = time.Now().Unix() + 3600
	var receiver = common.HexToAddress("0xbf00c30b93d76ab3d45625645b752b68199c8221")
	var hashPair = utils.NewSecretHashPair()
	log.Debug("secret = %s", hexutil.Encode([]byte(hashPair.Secret)))

	senderAuth := makeAuth(senderKey, client, 120000000)

	newContractTx, err := instance.NewContract(senderAuth, receiver, hashPair.Hash, big.NewInt(timeLock1Hour))
	if err != nil {
		log.Crit("Failed to initiate contract", err)
	}

	log.Info("transaction hash = %s", newContractTx.Hash().String())

	receipt, err := client.TransactionReceipt(context.Background(), newContractTx.Hash())
	if err != nil {
		log.Crit("Failed to get tx %s receipt", newContractTx.Hash(), err)
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

	log.Info("ContractId = %s", hexutil.Encode(logHTLCEvent.ContractId[:]))
	log.Info("Sender     = %s", logHTLCEvent.Sender.String())
	log.Info("Receiver   = %s", logHTLCEvent.Receiver.String())
	log.Info("Amount     = %s", logHTLCEvent.Amount)
	log.Info("TimeLock   = %s", logHTLCEvent.Timelock)
	log.Info("SecretHash = %s", hexutil.Encode(logHTLCEvent.Hashlock[:]))
}

func getContract(client *ethclient.Client, contractAddress string, contractId string) {
	log.Info("Get contract details from %s ...", contractId)
	if IsValidAddress(contractAddress) == false {
		log.Crit("Invalid address: %s", contractAddress)
	}
	instance := loadContract(client, common.HexToAddress(contractAddress))

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

func withdraw(client *ethclient.Client, contractAddress string, contractId string, secret string) {
	log.Info("Withdraw from contract %s ...", contractId)
	if IsValidAddress(contractAddress) == false {
		log.Crit("Invalid address: %s", contractAddress)
	}
	instance := loadContract(client, common.HexToAddress(contractAddress))

	receiverAuth := makeAuth(receiverKey, client, 0)

	receiverBalBefore, err := client.BalanceAt(context.Background(), receiverAuth.From, nil)
	if err != nil {
		log.Crit("Failed to get receiver balance: %s", err)
	}

	log.Info("Before withdraw balance of %s: %s", receiverAuth.From.String(), receiverBalBefore.String())

	var secretBytes [32]byte
	copy(secretBytes[:], hexutil.MustDecode(secret))

	WithdrawTx, err := instance.Withdraw(receiverAuth, common.HexToHash(contractId), secretBytes)
	if err != nil {
		log.Crit("Failed to withdraw with the specified contractId and secret: %s", err)
	}
	log.Info("transaction hash = %s", WithdrawTx.Hash().String())

	receiverBalAfter, err := client.BalanceAt(context.Background(), receiverAuth.From, nil)
	if err != nil {
		log.Crit("Failed to get receiver balance: %s", err)
	}
	log.Info("After withdraw balance of %s: %s", receiverAuth.From.String(), receiverBalAfter.String())
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

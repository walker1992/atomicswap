The HashedTimelock.sol is from [hashed-timelock-contract-ethereum](https://github.com/chatch/hashed-timelock-contract-ethereum/blob/master/contracts/HashedTimelock.sol)

```
localhost:atomicswap walker$ cd contract/
localhost:contract walker$ solc --abi --bin -o ./ HashedTimelock.sol
Compiler run successful. Artifact(s) can be found in directory ./.
localhost:contract walker$ ls
HashedTimelock.abi	HashedTimelock.sol	abigen
HashedTimelock.bin	README.md
localhost:contract walker$ ./abigen --pkg=htlc --out=./htlc.go --abi ./HashedTimelock.abi --bin ./HashedTimelock.bin
localhost:contract walker$ ls
HashedTimelock.abi	HashedTimelock.sol	abigen
HashedTimelock.bin	README.md		htlc.go
localhost:contract walker$ tree
.
├── HashedTimelock.abi
├── HashedTimelock.bin
├── HashedTimelock.sol
├── README.md
├── abigen
└── htlc.go

0 directories, 6 files
localhost:contract walker$ 
```
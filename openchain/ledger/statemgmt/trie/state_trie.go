/*
Licensed to the Apache Software Foundation (ASF) under one
or more contributor license agreements.  See the NOTICE file
distributed with this work for additional information
regarding copyright ownership.  The ASF licenses this file
to you under the Apache License, Version 2.0 (the
"License"); you may not use this file except in compliance
with the License.  You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing,
software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
KIND, either express or implied.  See the License for the
specific language governing permissions and limitations
under the License.
*/

package trie

import (
	"github.com/op/go-logging"
	"github.com/openblockchain/obc-peer/openchain/db"
	"github.com/openblockchain/obc-peer/openchain/ledger/statemgmt"
	"github.com/tecbot/gorocksdb"
)

var stateTrieLogger = logging.MustGetLogger("stateTrie")
var logHashOfEveryNode = false

type StateTrie struct {
	trieDelta           *trieDelta
	recomputeCryptoHash bool
}

func NewStateTrie() *StateTrie {
	return &StateTrie{}
}

func (stateTrie *StateTrie) Initialize() error {
	return nil
}

func (stateTrie *StateTrie) Get(chaincodeID string, key string) ([]byte, error) {
	trieNode, err := fetchTrieNodeFromDB(newTrieKey(chaincodeID, key))
	if err != nil {
		return nil, err
	}
	return trieNode.value, nil
}

func (stateTrie *StateTrie) PrepareWorkingSet(stateDelta *statemgmt.StateDelta) error {
	stateTrie.trieDelta = newTrieDelta(stateDelta)
	stateTrie.recomputeCryptoHash = true
	return nil
}

func (stateTrie *StateTrie) ClearWorkingSet() {
	stateTrie.trieDelta = nil
	stateTrie.recomputeCryptoHash = false
}

func (stateTrie *StateTrie) ComputeCryptoHash() ([]byte, error) {
	stateTrieLogger.Debug("Enter - ComputeCryptoHash()")
	if !stateTrie.recomputeCryptoHash {
		stateTrieLogger.Debug("No change since last time crypto-hash was computed. Returning result from last computation")
		return stateTrie.getLastComputedCryptoHash(), nil
	}
	lowestLevel := stateTrie.trieDelta.getLowestLevel()
	stateTrieLogger.Debug("Lowest level in trieDelta = [%d]", lowestLevel)
	for level := lowestLevel; level >= 0; level-- {
		changedNodes := stateTrie.trieDelta.deltaMap[level]
		for _, changedNode := range changedNodes {
			err := stateTrie.processChangedNode(changedNode)
			if err != nil {
				return nil, err
			}
		}
	}
	stateTrie.recomputeCryptoHash = false
	hash := stateTrie.getLastComputedCryptoHash()
	stateTrieLogger.Debug("Exit - ComputeCryptoHash()")
	return hash, nil
}

func (stateTrie *StateTrie) processChangedNode(changedNode *trieNode) error {
	stateTrieLogger.Debug("Enter - processChangedNode() for node [%s]", changedNode)
	dbNode, err := fetchTrieNodeFromDB(changedNode.trieKey)
	if err != nil {
		return err
	}
	if dbNode != nil {
		stateTrieLogger.Debug("processChangedNode() - merging attributes from db node [%s]", dbNode)
		changedNode.mergeMissingAttributesFrom(dbNode)
	}
	newCryptoHash := changedNode.computeCryptoHash()
	if changedNode.isRootNode() {
		changedNode.value = newCryptoHash
		stateTrieLogger.Debug("Exit - processChangedNode() for root-node [%s]", changedNode.trieKey)
		return nil
	}
	parentNode := stateTrie.trieDelta.getParentOf(changedNode)
	if parentNode == nil {
		parentNode = newTrieNode(changedNode.getParentTrieKey(), nil, false)
		stateTrie.trieDelta.addTrieNode(parentNode)
	}
	parentNode.setChildCryptoHash(changedNode.getIndexInParent(), newCryptoHash)
	if logHashOfEveryNode {
		stateTrieLogger.Debug("Hash for changedNode[%s]", changedNode)
		stateTrieLogger.Debug("%#v", newCryptoHash)
	}
	stateTrieLogger.Debug("Exit - processChangedNode() for node [%s]", changedNode)
	return nil
}

func (stateTrie *StateTrie) getLastComputedCryptoHash() []byte {
	if stateTrie.trieDelta == nil || stateTrie.trieDelta.getTrieRootNode() == nil {
		return nil
	}
	return stateTrie.trieDelta.getTrieRootNode().value
}

func (stateTrie *StateTrie) AddChangesForPersistence(writeBatch *gorocksdb.WriteBatch) error {
	if stateTrie.recomputeCryptoHash {
		_, err := stateTrie.ComputeCryptoHash()
		if err != nil {
			return err
		}
	}

	if stateTrie.trieDelta == nil {
		stateTrieLogger.Info("trieDelta is nil. Not writing anything to DB")
		return nil
	}

	openchainDB := db.GetDBHandle()
	lowestLevel := stateTrie.trieDelta.getLowestLevel()
	for level := lowestLevel; level >= 0; level-- {
		changedNodes := stateTrie.trieDelta.deltaMap[level]
		for _, changedNode := range changedNodes {
			if changedNode.markedForDeletion {
				writeBatch.DeleteCF(openchainDB.StateCF, changedNode.trieKey.getEncodedBytes())
				continue
			}
			serializedContent, err := changedNode.marshal()
			if err != nil {
				return err
			}
			writeBatch.PutCF(openchainDB.StateCF, changedNode.trieKey.getEncodedBytes(), serializedContent)
		}
	}
	stateTrieLogger.Debug("Added changes to DB")
	return nil
}

func (stateTrie *StateTrie) PerfHintKeyChanged(chaincodeID string, key string) {
	// nothing for now. Can perform pre-fetching of relevant data from db here.
}

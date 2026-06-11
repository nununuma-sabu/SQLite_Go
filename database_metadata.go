package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

var metadataMagic = []byte("SQLGODB1")

const (
	metadataMagicOffset   = 0
	metadataLengthOffset  = metadataMagicOffset + 8
	metadataPayloadOffset = metadataLengthOffset + 4
)

type databaseMetadata struct {
	Schema TableSchema       `json:"schema,omitempty"`
	Tables []TableDefinition `json:"tables,omitempty"`
}

func isMetadataPage(page []byte) bool {
	return bytes.Equal(page[metadataMagicOffset:metadataLengthOffset], metadataMagic)
}

func readDatabaseMetadata(page []byte) (databaseMetadata, error) {
	if !isMetadataPage(page) {
		return databaseMetadata{}, fmt.Errorf("metadata page magic mismatch")
	}

	payloadLength := binary.LittleEndian.Uint32(page[metadataLengthOffset:metadataPayloadOffset])
	if payloadLength == 0 || metadataPayloadOffset+int(payloadLength) > pageSize {
		return databaseMetadata{}, fmt.Errorf("metadata payload length is invalid")
	}

	var metadata databaseMetadata
	if err := json.Unmarshal(page[metadataPayloadOffset:metadataPayloadOffset+int(payloadLength)], &metadata); err != nil {
		return databaseMetadata{}, err
	}
	if len(metadata.Tables) == 0 {
		if !metadata.Schema.IsUsable() {
			return databaseMetadata{}, fmt.Errorf("metadata schema is invalid")
		}
		if metadata.Schema.SerializedRowSize() > leafNodeMaxPayloadSize {
			return databaseMetadata{}, fmt.Errorf("metadata schema row is too large")
		}
		metadata.Tables = []TableDefinition{{Schema: metadata.Schema, RootPageNum: defaultRootPageNum}}
	}
	for _, definition := range metadata.Tables {
		if !definition.Schema.IsUsable() {
			return databaseMetadata{}, fmt.Errorf("metadata schema is invalid")
		}
		if definition.RootPageNum == metadataPageNum {
			return databaseMetadata{}, fmt.Errorf("metadata root page is invalid")
		}
		if definition.Schema.SerializedRowSize() > leafNodeMaxPayloadSize {
			return databaseMetadata{}, fmt.Errorf("metadata schema row is too large")
		}
	}

	return metadata, nil
}

func writeDatabaseMetadata(page []byte, metadata databaseMetadata) error {
	if len(metadata.Tables) == 0 {
		if !metadata.Schema.IsUsable() {
			return fmt.Errorf("metadata schema is invalid")
		}
		if metadata.Schema.SerializedRowSize() > leafNodeMaxPayloadSize {
			return fmt.Errorf("metadata schema row is too large")
		}
		metadata.Tables = []TableDefinition{{Schema: metadata.Schema, RootPageNum: defaultRootPageNum}}
	}
	for _, definition := range metadata.Tables {
		if !definition.Schema.IsUsable() {
			return fmt.Errorf("metadata schema is invalid")
		}
		if definition.RootPageNum == metadataPageNum {
			return fmt.Errorf("metadata root page is invalid")
		}
		if definition.Schema.SerializedRowSize() > leafNodeMaxPayloadSize {
			return fmt.Errorf("metadata schema row is too large")
		}
	}

	payload, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if metadataPayloadOffset+len(payload) > pageSize {
		return fmt.Errorf("metadata payload is too large")
	}

	clear(page)
	copy(page[metadataMagicOffset:metadataLengthOffset], metadataMagic)
	binary.LittleEndian.PutUint32(page[metadataLengthOffset:metadataPayloadOffset], uint32(len(payload)))
	copy(page[metadataPayloadOffset:], payload)

	return nil
}

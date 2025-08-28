package cabinet

import (
//    "crypto/sha256"
//    "encoding/json"
//    "fmt"
)

// metaForChecksum creates a stable view of BaseBlock for checksum purposes.
// Volatile fields (Signature, Certificate, CRLRef, Valid, Checksum) are intentionally omitted.
type metaForChecksum struct {
    Name        string `json:"name"`
    Version     string `json:"version"`
    Description string `json:"description"`
    CreatedBy   string `json:"createdBy"`
    Timestamp   string `json:"timestamp"`
}

// calculateChecksum produces a deterministic HEX sha256 over a canonical JSON {meta,data}.
// NOTE: If 'data' contains map types, Go's json encoder does not guarantee key order in all versions.
// For fully stable results across runtimes, normalize maps before calling this.
//func calculateChecksum(meta *BaseBlock, data any) (string, error) {
//    m := metaForChecksum{
//        Name:        meta.Name,
//        Version:     meta.Version,
//        Description: meta.Description,
//        CreatedBy:   meta.CreatedBy,
//        Timestamp:   meta.Timestamp,
//    }
//    payload := struct {
//        Meta metaForChecksum `json:"meta"`
//        Data any             `json:"data"`
//    }{Meta: m, Data: data}
//
//    b, err := json.Marshal(payload)
//    if err != nil {
//        return "", err
//    }
//    sum := sha256.Sum256(b)
//    return fmt.Sprintf("%x", sum[:]), nil
//}

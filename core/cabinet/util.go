package cabinet

import (
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
)

// metaForChecksum creates a stable view of BaseBlock for checksum purposes.
// It intentionally omits Signature/Certificate/CRLRef/Valid/Checksum to avoid recursion and volatility.
type metaForChecksum struct {
    Name        string `json:"name"`
    Version     string `json:"version"`
    Description string `json:"description"`
    CreatedBy   string `json:"createdBy"`
    Timestamp   string `json:"timestamp"`
}

// calculateChecksum returns sha256(base64) over a canonical JSON of {meta,data} where meta excludes volatile fields.
func calculateChecksum(meta *BaseBlock, data any) (string, error) {
    m := metaForChecksum{
        Name: meta.Name, Version: meta.Version, Description: meta.Description,
        CreatedBy: meta.CreatedBy, Timestamp: meta.Timestamp,
    }
    payload := struct {
        Meta metaForChecksum `json:"meta"`
        Data any             `json:"data"`
    }{Meta: m, Data: data}

    b, err := json.Marshal(payload)
    if err != nil {
        return "", err
    }
    sum := sha256.Sum256(b)
    return base64.StdEncoding.EncodeToString(sum[:]), nil
}

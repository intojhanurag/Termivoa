package protocol

import (
	"encoding/base64"
	"errors"
	"regexp"
)

const (
	// MaxUploadBytes caps one uploaded image after the phone downscaled it.
	MaxUploadBytes = 5 * 1024 * 1024
	// MaxUploadChunkBytes is the decoded size of one gateway.upload.chunk.
	// Its base64 text plus the envelope stays under MaxInboundMessageBytes.
	MaxUploadChunkBytes = 128 * 1024
	// MaxUploadRequestText is the base64 length of a MaxUploadBytes file.
	MaxUploadRequestText = (MaxUploadBytes + 2) / 3 * 4
	maxUploadChunkText   = (MaxUploadChunkBytes + 2) / 3 * 4
)

var uploadPath = regexp.MustCompile(`^\.termivoa/uploads/[0-9]{8}-[0-9]{6}-[0-9a-f]{8}\.(jpg|png|webp|gif)$`)

// DecodeUploadData decodes standard padded base64 of 1..limit bytes.
func DecodeUploadData(text string, limit int) ([]byte, error) {
	if len(text) < 4 || len(text) > (limit+2)/3*4 {
		return nil, errors.New("upload data is outside bounds")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(text)
	if err != nil || len(data) == 0 || len(data) > limit {
		return nil, errors.New("upload data is not valid base64 within bounds")
	}
	return data, nil
}

// ValidateUploadResponse checks a saved-upload result.
func ValidateUploadResponse(response HTTPUploadResponse) error {
	if !uploadPath.MatchString(response.Path) {
		return errors.New("upload path is invalid")
	}
	if response.Size < 1 || response.Size > MaxUploadBytes {
		return errors.New("upload size is outside bounds")
	}
	switch response.ContentType {
	case ImageJPEG, ImagePNG, ImageWebp, ImageGIF:
		return nil
	}
	return errors.New("upload content type is not supported")
}

func validateGatewayUploadPayload(envelope GatewayRPCEnvelope) error {
	switch envelope.Type {
	case GatewayUploadBegin:
		payload, err := DecodeRPCPayload[GatewayRPCUploadBeginPayload](envelope)
		if err != nil {
			return err
		}
		if err := ValidateSecretText(payload.Credential); err != nil {
			return err
		}
		if !validUUID(payload.SessionID) || !validUUID(payload.Generation) {
			return errors.New("upload request contains a non-canonical UUID")
		}
		if payload.Size < 1 || payload.Size > MaxUploadBytes {
			return errors.New("upload size is outside bounds")
		}
		return nil
	case GatewayUploadChunk:
		payload, err := DecodeRPCPayload[GatewayRPCUploadChunkPayload](envelope)
		if err != nil {
			return err
		}
		if len(payload.Data) < 4 || len(payload.Data) > maxUploadChunkText {
			return errors.New("upload chunk is outside bounds")
		}
		return nil
	case GatewayUploadCommit:
		payload, err := DecodeRPCPayload[GatewayRPCCredentialPayload](envelope)
		if err != nil {
			return err
		}
		return ValidateSecretText(payload.Credential)
	case GatewayUploadBeginResult, GatewayUploadChunkResult:
		_, err := DecodeRPCPayload[BrokerRPCEmptyPayload](envelope)
		return err
	case GatewayUploadCommitResult:
		payload, err := DecodeRPCPayload[HTTPUploadResponse](envelope)
		if err != nil {
			return err
		}
		return ValidateUploadResponse(payload)
	}
	return errors.New("unknown gateway RPC message type")
}

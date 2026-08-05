package addon

import (
	"context"
	"fmt"
)

const (
	MaximumCatalogItems                      = 4096
	maximumExposableJSONDepth                = 16
	maximumExposableJSONNodes                = 128 << 10
	maximumExposableJSONObjectFields         = 128
	maximumExposableJSONArrayItems           = MaximumCatalogItems
	maximumExposableJSONStringBytes          = 64 << 10
	maximumExposableJSONAggregateStringBytes = 8 << 20
	maximumExposableJSONNumberBytes          = 1024
	maximumExposableJSONArrayElementBytes    = 256 << 10
)

type exposableJSONPolicy struct {
	maximumDepth                int
	maximumNodes                int
	maximumObjectFields         int
	maximumArrayItems           int
	maximumStringBytes          int
	maximumAggregateStringBytes int
	maximumNumberBytes          int
	maximumArrayElementBytes    int
}

var exposablePayloadPolicy = exposableJSONPolicy{
	maximumDepth:                maximumExposableJSONDepth,
	maximumNodes:                maximumExposableJSONNodes,
	maximumObjectFields:         maximumExposableJSONObjectFields,
	maximumArrayItems:           maximumExposableJSONArrayItems,
	maximumStringBytes:          maximumExposableJSONStringBytes,
	maximumAggregateStringBytes: maximumExposableJSONAggregateStringBytes,
	maximumNumberBytes:          maximumExposableJSONNumberBytes,
	maximumArrayElementBytes:    maximumExposableJSONArrayElementBytes,
}

func validateExposablePayloadComplexity(ctx context.Context, payload []byte) error {
	if len(payload) > maximumResourceBytes {
		return invalidExposablePayload("exceeds response byte limit")
	}
	return validateExposablePayloadComplexityWithPolicy(ctx, payload, exposablePayloadPolicy)
}

func validateExposablePayloadComplexityWithPolicy(ctx context.Context, payload []byte, policy exposableJSONPolicy) error {
	if ctx == nil {
		ctx = context.Background()
	}
	parser := exposableJSONParser{
		ctx:              ctx,
		payload:          payload,
		policy:           policy,
		nextContextCheck: 0,
	}
	if err := parser.checkContext(); err != nil {
		return err
	}
	if err := parser.skipWhitespace(); err != nil {
		return err
	}
	if err := parser.scanValue(1); err != nil {
		return err
	}
	if err := parser.skipWhitespace(); err != nil {
		return err
	}
	if parser.index != len(payload) {
		return invalidExposablePayload("contains invalid JSON")
	}
	return nil
}

type exposableJSONParser struct {
	ctx                  context.Context
	payload              []byte
	policy               exposableJSONPolicy
	index                int
	nodes                int
	aggregateStringBytes int
	nextContextCheck     int
}

func (parser *exposableJSONParser) scanValue(depth int) error {
	if err := parser.checkContext(); err != nil {
		return err
	}
	if parser.index >= len(parser.payload) {
		return invalidExposablePayload("contains invalid JSON")
	}
	parser.nodes++
	if parser.nodes > parser.policy.maximumNodes {
		return invalidExposablePayload("exceeds JSON node limit")
	}

	switch parser.payload[parser.index] {
	case '{':
		return parser.scanObject(depth)
	case '[':
		return parser.scanArray(depth)
	case '"':
		return parser.scanString()
	case 't':
		return parser.scanLiteral("true")
	case 'f':
		return parser.scanLiteral("false")
	case 'n':
		return parser.scanLiteral("null")
	default:
		return parser.scanNumber()
	}
}

func (parser *exposableJSONParser) scanObject(depth int) error {
	if depth > parser.policy.maximumDepth {
		return invalidExposablePayload("exceeds JSON depth limit")
	}
	parser.index++
	if err := parser.skipWhitespace(); err != nil {
		return err
	}
	if parser.consume('}') {
		return nil
	}

	fields := 0
	for {
		fields++
		if fields > parser.policy.maximumObjectFields {
			return invalidExposablePayload("exceeds JSON object field limit")
		}
		if parser.index >= len(parser.payload) || parser.payload[parser.index] != '"' {
			return invalidExposablePayload("contains invalid JSON")
		}
		if err := parser.scanString(); err != nil {
			return err
		}
		if err := parser.skipWhitespace(); err != nil {
			return err
		}
		if !parser.consume(':') {
			return invalidExposablePayload("contains invalid JSON")
		}
		if err := parser.skipWhitespace(); err != nil {
			return err
		}
		if err := parser.scanValue(depth + 1); err != nil {
			return err
		}
		if err := parser.skipWhitespace(); err != nil {
			return err
		}
		if parser.consume('}') {
			return nil
		}
		if !parser.consume(',') {
			return invalidExposablePayload("contains invalid JSON")
		}
		if err := parser.skipWhitespace(); err != nil {
			return err
		}
	}
}

func (parser *exposableJSONParser) scanArray(depth int) error {
	if depth > parser.policy.maximumDepth {
		return invalidExposablePayload("exceeds JSON depth limit")
	}
	parser.index++
	if err := parser.skipWhitespace(); err != nil {
		return err
	}
	if parser.consume(']') {
		return nil
	}

	items := 0
	for {
		items++
		if items > parser.policy.maximumArrayItems {
			return invalidExposablePayload("exceeds JSON array item limit")
		}
		itemStart := parser.index
		if err := parser.scanValue(depth + 1); err != nil {
			return err
		}
		if parser.index-itemStart > parser.policy.maximumArrayElementBytes {
			return invalidExposablePayload("exceeds JSON array element size limit")
		}
		if err := parser.skipWhitespace(); err != nil {
			return err
		}
		if parser.consume(']') {
			return nil
		}
		if !parser.consume(',') {
			return invalidExposablePayload("contains invalid JSON")
		}
		if err := parser.skipWhitespace(); err != nil {
			return err
		}
	}
}

func (parser *exposableJSONParser) scanString() error {
	parser.index++
	start := parser.index
	for parser.index < len(parser.payload) {
		if err := parser.checkContext(); err != nil {
			return err
		}
		character := parser.payload[parser.index]
		switch {
		case character == '"':
			stringBytes := parser.index - start
			if stringBytes > parser.policy.maximumStringBytes {
				return invalidExposablePayload("exceeds JSON string size limit")
			}
			if stringBytes > parser.policy.maximumAggregateStringBytes-parser.aggregateStringBytes {
				return invalidExposablePayload("exceeds aggregate JSON string limit")
			}
			parser.aggregateStringBytes += stringBytes
			parser.index++
			return nil
		case character == '\\':
			parser.index++
			if parser.index >= len(parser.payload) {
				return invalidExposablePayload("contains invalid JSON")
			}
			escaped := parser.payload[parser.index]
			if escaped == 'u' {
				if parser.index+4 >= len(parser.payload) {
					return invalidExposablePayload("contains invalid JSON")
				}
				for offset := 1; offset <= 4; offset++ {
					if !isJSONHex(parser.payload[parser.index+offset]) {
						return invalidExposablePayload("contains invalid JSON")
					}
				}
				parser.index += 5
			} else {
				if !isJSONEscape(escaped) {
					return invalidExposablePayload("contains invalid JSON")
				}
				parser.index++
			}
		case character < 0x20:
			return invalidExposablePayload("contains invalid JSON")
		default:
			parser.index++
		}
		if parser.index-start > parser.policy.maximumStringBytes {
			return invalidExposablePayload("exceeds JSON string size limit")
		}
	}
	return invalidExposablePayload("contains invalid JSON")
}

func (parser *exposableJSONParser) scanLiteral(literal string) error {
	if len(parser.payload)-parser.index < len(literal) || string(parser.payload[parser.index:parser.index+len(literal)]) != literal {
		return invalidExposablePayload("contains invalid JSON")
	}
	parser.index += len(literal)
	return nil
}

func (parser *exposableJSONParser) scanNumber() error {
	start := parser.index
	parser.consume('-')
	if parser.consume('0') {
		if parser.index < len(parser.payload) && isJSONDigit(parser.payload[parser.index]) {
			return invalidExposablePayload("contains invalid JSON")
		}
	} else {
		if parser.index >= len(parser.payload) || parser.payload[parser.index] < '1' || parser.payload[parser.index] > '9' {
			return invalidExposablePayload("contains invalid JSON")
		}
		if err := parser.scanNumberDigits(start); err != nil {
			return err
		}
	}
	if parser.consume('.') {
		fractionStart := parser.index
		if err := parser.scanNumberDigits(start); err != nil {
			return err
		}
		if parser.index == fractionStart {
			return invalidExposablePayload("contains invalid JSON")
		}
	}
	if parser.index < len(parser.payload) && (parser.payload[parser.index] == 'e' || parser.payload[parser.index] == 'E') {
		parser.index++
		if parser.index < len(parser.payload) && (parser.payload[parser.index] == '+' || parser.payload[parser.index] == '-') {
			parser.index++
		}
		exponentStart := parser.index
		if err := parser.scanNumberDigits(start); err != nil {
			return err
		}
		if parser.index == exponentStart {
			return invalidExposablePayload("contains invalid JSON")
		}
	}
	if parser.index-start > parser.policy.maximumNumberBytes {
		return invalidExposablePayload("exceeds JSON number size limit")
	}
	if parser.index == start {
		return invalidExposablePayload("contains invalid JSON")
	}
	return nil
}

func (parser *exposableJSONParser) scanNumberDigits(start int) error {
	for parser.index < len(parser.payload) && isJSONDigit(parser.payload[parser.index]) {
		parser.index++
		if parser.index-start > parser.policy.maximumNumberBytes {
			return invalidExposablePayload("exceeds JSON number size limit")
		}
		if err := parser.checkContext(); err != nil {
			return err
		}
	}
	return nil
}

func (parser *exposableJSONParser) skipWhitespace() error {
	for parser.index < len(parser.payload) {
		if err := parser.checkContext(); err != nil {
			return err
		}
		switch parser.payload[parser.index] {
		case ' ', '\n', '\r', '\t':
			parser.index++
		default:
			return nil
		}
	}
	return nil
}

func (parser *exposableJSONParser) consume(character byte) bool {
	if parser.index >= len(parser.payload) || parser.payload[parser.index] != character {
		return false
	}
	parser.index++
	return true
}

func (parser *exposableJSONParser) checkContext() error {
	if parser.index < parser.nextContextCheck {
		return nil
	}
	select {
	case <-parser.ctx.Done():
		return parser.ctx.Err()
	default:
		parser.nextContextCheck = parser.index + 4<<10
		return nil
	}
}

func invalidExposablePayload(reason string) error {
	return fmt.Errorf("%w: addon payload %s", ErrInvalidResponse, reason)
}

func isJSONDigit(character byte) bool {
	return character >= '0' && character <= '9'
}

func isJSONHex(character byte) bool {
	return isJSONDigit(character) || character >= 'a' && character <= 'f' || character >= 'A' && character <= 'F'
}

func isJSONEscape(character byte) bool {
	switch character {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	default:
		return false
	}
}

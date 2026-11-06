// Package imagedec registers the image decoders dmcode reads, so "is this
// decodable" has exactly one answer everywhere.
//
// It exists because two packages need the same fact: imgprev turns clipboard
// and dropped bytes into pictures, and clipimg decides which clipboard tool's
// answer decodes. A list duplicated between them is a list that drifts —
// the bug the Windows clipimg already refuses to have twice — so neither
// package owns it.
package imagedec

import (
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

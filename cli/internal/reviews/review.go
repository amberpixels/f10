package reviews

import "errors"

// ErrReviewChanged is settle refusing a review rewritten since it was read:
// a sticky comment edited by a re-review is a new, unread review.
var ErrReviewChanged = errors.New("the review changed since it was read")

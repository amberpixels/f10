// Package drivestore keeps a drive durable: a record per drive under the
// storage root, and a lease per task. The lease keeps two drives, or a
// drive and `f10 finish`, from acting on one task at once.
package drivestore

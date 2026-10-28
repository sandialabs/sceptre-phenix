// Package builder stores Builder drafts, published documents and the icon,
// template and layer set libraries through [phenix/store.RecordStore].
// Every draft mutation is a compare-and-swap on the revision the caller saw,
// and a save commits in one transaction. The package does no authorization:
// the web layer passes a trusted, already authorized actor.
package builder

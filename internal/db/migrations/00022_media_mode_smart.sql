-- +goose Up
-- +goose NO TRANSACTION
-- Smart media (D-84): anchor only when the legs cannot agree on a codec.
-- With 'smart' the route decides per call: if a codec the caller offered is
-- allowed by both the customer and the carrier, the call is proxied (RTP
-- relayed untouched, no decode and re-encode); otherwise it falls back to
-- anchoring, which is the only way to bridge two different codecs.
ALTER TYPE media_mode_kind ADD VALUE IF NOT EXISTS 'smart';

-- +goose Down
-- +goose NO TRANSACTION
-- Enum labels cannot be dropped in PostgreSQL; move the rows back to anchor
-- so nothing references the label, and leave the label in place.
UPDATE customers SET media_mode = 'anchor' WHERE media_mode = 'smart';
UPDATE carriers SET media_mode = 'anchor' WHERE media_mode = 'smart';

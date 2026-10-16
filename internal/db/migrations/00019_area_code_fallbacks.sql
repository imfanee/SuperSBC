-- +goose Up
-- Country level fallbacks (D-76), so a call into a country we carry is never
-- unlabelled just because its town is missing from the table. Longest prefix
-- wins, so every specific entry still takes precedence.
INSERT INTO area_codes (country_code, area_code, country, description, operator, kind, notes) VALUES
    ('243', '', 'DR Congo', 'DR Congo', '', 'other', 'fallback: no more specific range matched'),
    ('91', '', 'India', 'India', '', 'other', 'fallback: no more specific range matched')
ON CONFLICT (country_code, area_code) DO NOTHING;

UPDATE cdrs d SET area_prefix = a.prefix, area_description = a.description, operator_name = a.operator
FROM area_codes a
WHERE a.enabled AND d.called_number <> '' AND d.area_prefix IS NULL AND d.called_number LIKE a.prefix || '%'
  AND length(a.prefix) = (SELECT max(length(x.prefix)) FROM area_codes x
                          WHERE x.enabled AND d.called_number LIKE x.prefix || '%');

-- +goose Down
DELETE FROM area_codes WHERE area_code = '' AND country_code IN ('243', '91');

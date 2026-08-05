
LOCK TABLE access_categories IN ACCESS EXCLUSIVE MODE;

UPDATE access_categories
SET icon = regexp_replace(
    regexp_replace(icon, '[-_]+', '-', 'g'),
    '-+$',
    ''
)
WHERE icon IS NOT NULL;
SET CONSTRAINTS access_categories_require_default IMMEDIATE;

ALTER TABLE access_categories
    DROP CONSTRAINT IF EXISTS access_categories_icon_check,
    ADD CONSTRAINT access_categories_icon_check
        CHECK (
            icon IS NULL
            OR (
                char_length(icon) <= 64
                AND icon ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'
            )
        );

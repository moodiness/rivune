DO $migration$
BEGIN
    ALTER TABLE profiles
        ADD COLUMN enabled boolean NOT NULL DEFAULT true,
        ADD COLUMN available_from date,
        ADD COLUMN available_until date,
        ADD COLUMN access_start_time time without time zone,
        ADD COLUMN access_end_time time without time zone,
        ADD COLUMN access_timezone text NOT NULL DEFAULT 'UTC';

    ALTER TABLE profiles
        ADD CONSTRAINT profiles_availability_dates_ordered CHECK (
            available_from IS NULL OR available_until IS NULL OR available_from <= available_until
        ),
        ADD CONSTRAINT profiles_access_hours_paired CHECK (
            (access_start_time IS NULL AND access_end_time IS NULL)
            OR (access_start_time IS NOT NULL AND access_end_time IS NOT NULL AND access_start_time <> access_end_time)
        ),
        ADD CONSTRAINT profiles_access_timezone_present CHECK (
            access_timezone = btrim(access_timezone) AND access_timezone <> ''
        );
END
$migration$;

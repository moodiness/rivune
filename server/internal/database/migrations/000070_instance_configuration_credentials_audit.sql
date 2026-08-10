ALTER TABLE instances
    ADD COLUMN configuration_revision bigint NOT NULL DEFAULT 0 CHECK (configuration_revision >= 0),
    ADD COLUMN legacy_environment_imported_at timestamptz,
    ADD COLUMN legacy_instance_setting_keys text[] NOT NULL DEFAULT ARRAY[]::text[];

UPDATE instances i
SET legacy_instance_setting_keys = ARRAY(
    SELECT key
    FROM jsonb_object_keys(s.settings) AS key
    ORDER BY key
)
FROM instance_settings s
WHERE s.instance_id = i.id;

UPDATE instance_settings
SET schema_version = 2,
    settings = (
        settings
        - ARRAY[
            'tmdbAccessToken', 'fanartApiKey', 'mdblistApiKey', 'tvdbApiKey', 'tvdbPin',
            'traktClientId', 'traktClientSecret', 'simklClientId',
            'TMDBAccessToken', 'FanartAPIKey', 'MDBListAPIKey', 'TVDBAPIKey', 'TVDBPIN',
            'TraktClientID', 'TraktClientSecret', 'SimklClientID'
        ]::text[]
    ) || jsonb_build_object(
        'timezone', COALESCE(settings -> 'timezone', '"UTC"'::jsonb),
        'jellyfinEnabled', COALESCE(settings -> 'jellyfinEnabled', 'false'::jsonb),
        'jellyfinDebug', COALESCE(settings -> 'jellyfinDebug', 'false'::jsonb),
        'hardwareAcceleration', COALESCE(settings -> 'hardwareAcceleration', '"auto"'::jsonb),
        'transcodeMaxBitrateKbps', COALESCE(settings -> 'transcodeMaxBitrateKbps', '12000'::jsonb),
        'mediaMaxStorageMB', COALESCE(settings -> 'mediaMaxStorageMB', '20480'::jsonb),
        'artworkMaxStorageMB', COALESCE(settings -> 'artworkMaxStorageMB', '20480'::jsonb),
        'allowTranscoding', COALESCE(settings -> 'allowTranscoding', 'true'::jsonb)
    ),
    updated_at = now();

ALTER TABLE instance_settings
    ALTER COLUMN schema_version SET DEFAULT 2,
    ALTER COLUMN settings SET DEFAULT '{"timezone":"UTC","jellyfinEnabled":false,"jellyfinDebug":false,"hardwareAcceleration":"auto","transcodeMaxBitrateKbps":12000,"mediaMaxStorageMB":20480,"artworkMaxStorageMB":20480,"allowTranscoding":true}'::jsonb,
    DROP CONSTRAINT IF EXISTS instance_settings_schema_v2_runtime_values,
    ADD CONSTRAINT instance_settings_schema_v2_runtime_values CHECK (
        schema_version <> 2 OR (
            jsonb_typeof(settings) = 'object'
            AND jsonb_typeof(settings -> 'timezone') = 'string'
            AND char_length(settings ->> 'timezone') BETWEEN 1 AND 255
            AND jsonb_typeof(settings -> 'jellyfinEnabled') = 'boolean'
            AND jsonb_typeof(settings -> 'jellyfinDebug') = 'boolean'
            AND jsonb_typeof(settings -> 'hardwareAcceleration') = 'string'
            AND settings ->> 'hardwareAcceleration' IN ('auto', 'software', 'vaapi', 'qsv', 'nvenc')
            AND jsonb_typeof(settings -> 'transcodeMaxBitrateKbps') = 'number'
            AND (settings ->> 'transcodeMaxBitrateKbps')::integer BETWEEN 64 AND 200000
            AND jsonb_typeof(settings -> 'mediaMaxStorageMB') = 'number'
            AND (settings ->> 'mediaMaxStorageMB')::integer BETWEEN 512 AND 102400
            AND jsonb_typeof(settings -> 'artworkMaxStorageMB') = 'number'
            AND (settings ->> 'artworkMaxStorageMB')::integer BETWEEN 256 AND 102400
            AND jsonb_typeof(settings -> 'allowTranscoding') = 'boolean'
            AND NOT (settings ?| ARRAY[
                'tmdbAccessToken', 'fanartApiKey', 'mdblistApiKey', 'tvdbApiKey', 'tvdbPin',
                'traktClientId', 'traktClientSecret', 'simklClientId',
                'TMDBAccessToken', 'FanartAPIKey', 'MDBListAPIKey', 'TVDBAPIKey', 'TVDBPIN',
                'TraktClientID', 'TraktClientSecret', 'SimklClientID'
            ])
        )
    );

CREATE TABLE instance_integration_credentials (
    instance_id smallint NOT NULL DEFAULT 1 REFERENCES instances(id) ON DELETE CASCADE CHECK (instance_id = 1),
    name text NOT NULL CHECK (name IN (
        'tmdbAccessToken', 'fanartApiKey', 'mdblistApiKey', 'tvdbApiKey', 'tvdbPin',
        'traktClientId', 'traktClientSecret', 'simklClientId'
    )),
    ciphertext bytea,
    cipher_version smallint,
    encryption_key_version integer,
    generation bigint NOT NULL CHECK (generation > 0),
    CHECK (
        (ciphertext IS NULL AND cipher_version IS NULL AND encryption_key_version IS NULL)
        OR (octet_length(ciphertext) >= 29 AND cipher_version = 1 AND encryption_key_version > 0)
    ),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (instance_id, name)
);

CREATE TABLE instance_configuration_audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    instance_id smallint NOT NULL DEFAULT 1 REFERENCES instances(id) ON DELETE CASCADE CHECK (instance_id = 1),
    revision bigint NOT NULL CHECK (revision > 0),
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    action text NOT NULL CHECK (action IN ('settings.updated', 'integrations.updated', 'legacy_environment.imported')),
    changed_keys text[] NOT NULL,
    snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (instance_id, revision)
);

CREATE INDEX instance_configuration_audit_events_instance_id_id_idx
    ON instance_configuration_audit_events (instance_id, id DESC);

ALTER TABLE profile_tracking_accounts
    ADD COLUMN cipher_version smallint NOT NULL DEFAULT 1 CHECK (cipher_version = 1),
    ADD COLUMN encryption_key_version integer NOT NULL DEFAULT 1 CHECK (encryption_key_version > 0);

ALTER TABLE profile_tracking_authorizations
    ADD COLUMN cipher_version smallint NOT NULL DEFAULT 1 CHECK (cipher_version = 1),
    ADD COLUMN encryption_key_version integer NOT NULL DEFAULT 1 CHECK (encryption_key_version > 0);

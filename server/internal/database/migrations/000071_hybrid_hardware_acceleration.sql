ALTER TABLE instance_settings
    DROP CONSTRAINT IF EXISTS instance_settings_schema_v2_runtime_values,
    ADD CONSTRAINT instance_settings_schema_v2_runtime_values CHECK (
        schema_version <> 2 OR (
            jsonb_typeof(settings) = 'object'
            AND jsonb_typeof(settings -> 'timezone') = 'string'
            AND char_length(settings ->> 'timezone') BETWEEN 1 AND 255
            AND jsonb_typeof(settings -> 'jellyfinEnabled') = 'boolean'
            AND jsonb_typeof(settings -> 'jellyfinDebug') = 'boolean'
            AND jsonb_typeof(settings -> 'hardwareAcceleration') = 'string'
            AND settings ->> 'hardwareAcceleration' IN ('auto', 'software', 'hybrid', 'vaapi', 'qsv', 'nvenc')
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

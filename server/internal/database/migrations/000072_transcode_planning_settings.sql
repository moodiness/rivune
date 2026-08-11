UPDATE instance_settings
SET schema_version = 3,
    settings = settings || jsonb_build_object(
		'preferredTranscodeVideoCodec', 'auto',
		'transcodeQualityPreset', 'balanced',
		'transcodeConcurrency', 4
    ),
    updated_at = now()
WHERE schema_version = 2;

ALTER TABLE instance_settings
    ALTER COLUMN schema_version SET DEFAULT 3,
    ALTER COLUMN settings SET DEFAULT '{"timezone":"UTC","jellyfinEnabled":false,"jellyfinDebug":false,"hardwareAcceleration":"auto","preferredTranscodeVideoCodec":"auto","transcodeQualityPreset":"balanced","transcodeConcurrency":4,"transcodeMaxBitrateKbps":12000,"mediaMaxStorageMB":20480,"artworkMaxStorageMB":20480,"allowTranscoding":true}'::jsonb,
    DROP CONSTRAINT IF EXISTS instance_settings_schema_v2_runtime_values,
    ADD CONSTRAINT instance_settings_schema_v3_runtime_values CHECK (
        schema_version <> 3 OR (
            jsonb_typeof(settings) = 'object'
            AND jsonb_typeof(settings -> 'timezone') = 'string'
            AND char_length(settings ->> 'timezone') BETWEEN 1 AND 255
            AND jsonb_typeof(settings -> 'jellyfinEnabled') = 'boolean'
            AND jsonb_typeof(settings -> 'jellyfinDebug') = 'boolean'
            AND jsonb_typeof(settings -> 'hardwareAcceleration') = 'string'
            AND settings ->> 'hardwareAcceleration' IN ('auto', 'software', 'hybrid', 'vaapi', 'qsv', 'nvenc', 'amf')
            AND jsonb_typeof(settings -> 'preferredTranscodeVideoCodec') = 'string'
            AND settings ->> 'preferredTranscodeVideoCodec' IN ('auto', 'h264', 'hevc', 'av1')
            AND jsonb_typeof(settings -> 'transcodeQualityPreset') = 'string'
            AND settings ->> 'transcodeQualityPreset' IN ('speed', 'balanced', 'quality')
            AND jsonb_typeof(settings -> 'transcodeConcurrency') = 'number'
            AND (settings ->> 'transcodeConcurrency')::integer BETWEEN 1 AND 32
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

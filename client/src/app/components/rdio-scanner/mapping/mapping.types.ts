/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

export interface MappingIntegrationConfig {
    geocodeCacheMaxAgeDays?: number;
    autoLearnKnownPlaces?: boolean;
    maxGeocodeCandidatesPerCall?: number;
    /** Empty = use Admin → External Integrations → OpenAI chat model */
    openAIModel?: string;
    /** Retained for interface stability; mapping always uses the built-in rules
     *  extractor + outbound geocode chain (Thinline Geocoding API → Census). */
    mappingEngine?: string;
    /** Show Census boundary overlays on the incident map. */
    mapBoundariesEnabled?: boolean;
    /** Enabled overlay layers: county, place, cousub. */
    mapBoundaryLayers?: string[];
    /** When phrase matching finds no call nature, use OpenAI to classify from the category list. */
    callNatureOpenAIClassify?: boolean;
    /** Suggest local transcript jargon under call natures (admin must Accept). */
    callNaturePhraseLearn?: boolean;
    /** Distinct calls required before a phrase suggestion is ready (default 3). */
    callNaturePhraseLearnMinSightings?: number;
    /** Skip geocoding and drop map pins for catch-all UNKNOWN PROBLEM natures. */
    suppressUnknownNaturePins?: boolean;
    /** Append incident-mapping location context to the STT prompt (Gemini/Whisper/etc.). */
    sendLocationContext?: boolean;
}

export interface MappingToneSetLocationRow {
    talkgroupId: number;
    talkgroupLabel: string;
    toneSetId: string;
    label: string;
    geoCity?: string;
    geoLat?: number;
    geoLon?: number;
    geoRadiusMiles?: number;
    locationContext?: string;
}

export interface MappingToneSetLocationList {
    toneSets: MappingToneSetLocationRow[];
    total: number;
}

export interface MappingToneSetLocationApply {
    talkgroupId: number;
    toneSetId: string;
    geoCity?: string;
    geoLat?: number;
    geoLon?: number;
    geoRadiusMiles?: number;
    locationContext?: string;
    clear?: boolean;
}

export interface MappingToneSetLocationApplyResult {
    applied: number;
    cleared: number;
}

export interface MappingToneSetLocationSuggest {
    talkgroupId: number;
    toneSetId: string;
    label?: string;
    geoCity?: string;
    geoLat?: number;
    geoLon?: number;
    geoRadiusMiles?: number;
    locationContext?: string;
    source?: 'boundary' | 'search' | 'gemini_only' | 'skipped' | string;
    error?: string;
}

export interface MappingToneSetLocationSuggestResult {
    toneSets: MappingToneSetLocationSuggest[];
    filled: number;
    skipped: number;
    failed: number;
    total: number;
}

export interface MappingTalkgroupLocationRow {
    talkgroupId: number;
    talkgroupLabel: string;
    inherit?: boolean;
    enabled?: boolean;
    geoCity?: string;
    geoLat?: number;
    geoLon?: number;
    geoRadiusMiles?: number;
    locationContext?: string;
}

export interface MappingTalkgroupLocationList {
    talkgroups: MappingTalkgroupLocationRow[];
    total: number;
}

export interface MappingTalkgroupLocationApply {
    talkgroupId: number;
    geoCity?: string;
    geoLat?: number;
    geoLon?: number;
    geoRadiusMiles?: number;
    locationContext?: string;
    clear?: boolean;
}

export interface MappingTalkgroupLocationApplyResult {
    applied: number;
    cleared: number;
}

export interface MappingTalkgroupLocationSuggest {
    talkgroupId: number;
    talkgroupLabel?: string;
    geoCity?: string;
    geoLat?: number;
    geoLon?: number;
    geoRadiusMiles?: number;
    locationContext?: string;
    source?: 'boundary' | 'search' | 'gemini_only' | 'skipped' | string;
    error?: string;
}

export interface MappingTalkgroupLocationSuggestResult {
    talkgroups: MappingTalkgroupLocationSuggest[];
    filled: number;
    skipped: number;
    failed: number;
    total: number;
}

export interface IncidentMappingConfig {
    enabled?: boolean;
    inherit?: boolean;
    geoCity?: string;
    geoLat?: number;
    geoLon?: number;
    geoRadiusMiles?: number;
    locationContext?: string;
    /** When Gemini is the STT provider, also extract a short scene address for geocoding. */
    extractAddressWithGemini?: boolean;
}

export interface MappingStreetRow {
    id: number;
    streetName: string;
    talkgroupId?: number;
}

export interface MappingCorrectionRow {
    id: number;
    badName: string;
    correctName: string;
    talkgroupId?: number;
}

export interface MappingPlaceRow {
    id: number;
    displayName: string;
    lat: number;
    lon: number;
    addressHint?: string;
    source?: string;
    talkgroupId?: number;
}

export interface MappingDataResponse {
    streets: MappingStreetRow[];
    corrections: MappingCorrectionRow[];
    places: MappingPlaceRow[];
    stats?: {
        streets?: number;
        corrections?: number;
        places?: number;
        cacheEntries?: number;
    };
}

export interface IncidentRecord {
    callId: number;
    systemId: number;
    talkgroupId: number;
    timestamp: number;
    address?: string;
    crossStreet1?: string;
    crossStreet2?: string;
    nature?: string;
    commonName?: string;
    lat: number;
    lon: number;
    status?: string;
    source?: string;
    transcript?: string;
    systemLabel?: string;
    talkgroupLabel?: string;
    /** Talkgroup tag label (Fire, Law, EMS, …) */
    tagLabel?: string;
    /** Admin default tag color from server config */
    tagColor?: string;
    /** Additional calls at the same location (multi-channel dispatch). */
    relatedCallIds?: number[];
    /** Channel labels for related calls (same order as relatedCallIds). */
    relatedChannels?: string[];
}

export interface MappingBoundaryImportStatus {
    active: boolean;
    phase?: string;
    message?: string;
    total: number;
    completed: number;
    percent: number;
    done: boolean;
    error?: string;
    counts?: Record<string, number>;
}

export interface MappingBoundaryStats {
    total: number;
    byLayer?: Record<string, number>;
    byState?: Record<string, number>;
}

export interface MapBoundaryCollection {
    enabled: boolean;
    layers?: string[];
    colors?: string[];
    type: string;
    features: MapBoundaryFeature[];
}

export interface MapBoundaryFeature {
    type: string;
    properties: {
        geoid: string;
        name: string;
        layer: string;
        colorIndex: number;
    };
    geometry: object;
}

export interface UsStateOption {
    fips: string;
    name: string;
    abbr: string;
}

/** US states + DC for Census boundary import (FIPS codes). */
export const US_STATE_FIPS_OPTIONS: UsStateOption[] = [
    { fips: '01', name: 'Alabama', abbr: 'AL' }, { fips: '02', name: 'Alaska', abbr: 'AK' },
    { fips: '04', name: 'Arizona', abbr: 'AZ' }, { fips: '05', name: 'Arkansas', abbr: 'AR' },
    { fips: '06', name: 'California', abbr: 'CA' }, { fips: '08', name: 'Colorado', abbr: 'CO' },
    { fips: '09', name: 'Connecticut', abbr: 'CT' }, { fips: '10', name: 'Delaware', abbr: 'DE' },
    { fips: '11', name: 'District of Columbia', abbr: 'DC' }, { fips: '12', name: 'Florida', abbr: 'FL' },
    { fips: '13', name: 'Georgia', abbr: 'GA' }, { fips: '15', name: 'Hawaii', abbr: 'HI' },
    { fips: '16', name: 'Idaho', abbr: 'ID' }, { fips: '17', name: 'Illinois', abbr: 'IL' },
    { fips: '18', name: 'Indiana', abbr: 'IN' }, { fips: '19', name: 'Iowa', abbr: 'IA' },
    { fips: '20', name: 'Kansas', abbr: 'KS' }, { fips: '21', name: 'Kentucky', abbr: 'KY' },
    { fips: '22', name: 'Louisiana', abbr: 'LA' }, { fips: '23', name: 'Maine', abbr: 'ME' },
    { fips: '24', name: 'Maryland', abbr: 'MD' }, { fips: '25', name: 'Massachusetts', abbr: 'MA' },
    { fips: '26', name: 'Michigan', abbr: 'MI' }, { fips: '27', name: 'Minnesota', abbr: 'MN' },
    { fips: '28', name: 'Mississippi', abbr: 'MS' }, { fips: '29', name: 'Missouri', abbr: 'MO' },
    { fips: '30', name: 'Montana', abbr: 'MT' }, { fips: '31', name: 'Nebraska', abbr: 'NE' },
    { fips: '32', name: 'Nevada', abbr: 'NV' }, { fips: '33', name: 'New Hampshire', abbr: 'NH' },
    { fips: '34', name: 'New Jersey', abbr: 'NJ' }, { fips: '35', name: 'New Mexico', abbr: 'NM' },
    { fips: '36', name: 'New York', abbr: 'NY' }, { fips: '37', name: 'North Carolina', abbr: 'NC' },
    { fips: '38', name: 'North Dakota', abbr: 'ND' }, { fips: '39', name: 'Ohio', abbr: 'OH' },
    { fips: '40', name: 'Oklahoma', abbr: 'OK' }, { fips: '41', name: 'Oregon', abbr: 'OR' },
    { fips: '42', name: 'Pennsylvania', abbr: 'PA' }, { fips: '44', name: 'Rhode Island', abbr: 'RI' },
    { fips: '45', name: 'South Carolina', abbr: 'SC' }, { fips: '46', name: 'South Dakota', abbr: 'SD' },
    { fips: '47', name: 'Tennessee', abbr: 'TN' }, { fips: '48', name: 'Texas', abbr: 'TX' },
    { fips: '49', name: 'Utah', abbr: 'UT' }, { fips: '50', name: 'Vermont', abbr: 'VT' },
    { fips: '51', name: 'Virginia', abbr: 'VA' }, { fips: '53', name: 'Washington', abbr: 'WA' },
    { fips: '54', name: 'West Virginia', abbr: 'WV' }, { fips: '55', name: 'Wisconsin', abbr: 'WI' },
    { fips: '56', name: 'Wyoming', abbr: 'WY' },
];

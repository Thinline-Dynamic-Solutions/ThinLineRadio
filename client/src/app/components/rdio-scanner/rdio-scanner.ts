/*
 * *****************************************************************************
 * Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>
 * ****************************************************************************
 */

import { Subscription } from "rxjs";

export interface RdioScannerAvoidOptions {
    all?: boolean;
    call?: RdioScannerCall;
    minutes?: number;
    status?: boolean;
    system?: RdioScannerSystem;
    talkgroup?: RdioScannerTalkgroup;
}

export interface RdioScannerAlerts {
    [key: string]: RdioScannerOscillatorData[];
}

export enum RdioScannerBeepStyle {
    Activate = 'activate',
    Deactivate = 'deactivate',
    Denied = 'denied',
}

export interface RdioScannerCall {
    audio?: {
        type: 'Buffer';
        data: number[];
    };
    audioName?: string;
    audioType?: string;
    dateTime: Date;
    delayed: boolean;
    frequencies?: RdioScannerCallFrequency[];
    frequency?: number;
    groupsData?: RdioScannerGroupData[];
    id: number;
    patches: number[];
    source?: number;
    sources?: RdioScannerCallSource[];
    system: number;
    tagData?: RdioScannerTagData;
    talkgroup: number;
    talkgroupData?: RdioScannerTalkgroup;
    systemData?: RdioScannerSystem;
    site?: number;
    siteData?: {
        id: number;
        label: string;
        siteRef: number;
        systemId: number;
    };
    hasTones?: boolean;
    toneSequence?: RdioScannerToneSequence;
    transcript?: string;
    transcriptConfidence?: number;
    transcriptionStatus?: string;
}

export interface RdioScannerToneSequence {
    tones: RdioScannerTone[];
    matchedToneSet?: RdioScannerToneSet;
}

export interface RdioScannerTone {
    frequency: number;
    duration: number;
    type: 'a' | 'b' | 'long';
}

export interface RdioScannerToneSet {
    id: string;
    label: string;
    aTone?: RdioScannerToneSpec;
    bTone?: RdioScannerToneSpec;
    longTone?: RdioScannerToneSpec;
    tolerance?: number;
    minDuration?: number;
}

export interface RdioScannerToneSpec {
    frequency: number;
    minDuration: number;
    maxDuration?: number;
}

export interface RdioScannerAlert {
    alertId: number;
    callId: number;
    systemId: number;
    talkgroupId: number;
    alertType: 'tone' | 'keyword' | 'tone+keyword';
    toneDetected: boolean;
    keywordsMatched?: string; // JSON array
    transcriptSnippet?: string;
    transcript?: string;
    alertSummary?: string;
    transcriptionStatus?: string;
    createdAt: number;
    systemLabel?: string;
    talkgroupLabel?: string;
    talkgroupName?: string;
    matchedToneSetName?: string; // Backward compatibility - first tone set
    matchedToneSetNames?: string[]; // All matched tone sets for stacked tones
}

export interface RdioScannerTranscript {
    callId: number;
    systemId: number;
    talkgroupId: number;
    transcript: string;
    transcriptionStatus?: string;
    timestamp?: number;
    alertSummary?: string;
    systemLabel?: string;
    talkgroupLabel?: string;
    talkgroupName?: string;
}

export interface RdioScannerAlertPreference {
    userId: number;
    systemId?: number;        // Optional - used internally, not sent to API
    systemRef?: number;       // Used when sending to API (avoids ID collision)
    talkgroupId?: number;     // Optional - used internally, not sent to API
    talkgroupRef?: number;    // Used when sending to API (avoids ID collision)
    alertEnabled: boolean;
    toneAlerts: boolean;
    keywordAlerts: boolean;
    keywords: string[];
    keywordListIds: number[];
    toneSetIds?: string[];
}

export interface RdioScannerKeywordList {
    id: number;
    label: string;
    description?: string;
    keywords: string[];
    order: number;
    createdAt: number;
}

export interface RdioScannerCallFrequency {
    dbm?: number;
    errorCount?: number;
    freq?: number;
    len?: number;
    pos?: number;
    spikeCount?: number;
}

export interface RdioScannerCallSource {
    pos?: number;
    src?: number;
    tag?: string; // P25 talker alias / dynamic unit label
}

export interface RdioScannerCategory {
    label: string;
    status: RdioScannerCategoryStatus;
    type: RdioScannerCategoryType;
}

export enum RdioScannerCategoryStatus {
    Off = 'off',
    On = 'on',
    Partial = 'partial',
}

export enum RdioScannerCategoryType {
    Group = 'group',
    Tag = 'tag',
}

export interface RdioScannerConfig {
    alerts?: RdioScannerAlerts;
    branding?: string;
    dimmerDelay: number | false;
    email?: string;
    groups: { [key: string]: { [key: number]: number[] } };
    groupsData: RdioScannerGroupData[];
    keypadBeeps: RdioScannerKeypadBeeps | undefined;
    options?: {
        userRegistrationEnabled?: boolean;
        stripePaywallEnabled?: boolean;
        emailServiceEnabled?: boolean;
        emailSmtpHost?: string;
        emailSmtpPort?: number;
        emailSmtpUsername?: string;
        emailSmtpPassword?: string;
        emailSmtpFromEmail?: string;
        emailSmtpFromName?: string;
        emailSmtpUseTLS?: boolean;
        emailLogoFilename?: string;
        emailLogoBorderRadius?: string;
        stripePublishableKey?: string;
        stripePriceId?: string;
        pricingOptions?: Array<{
            priceId: string;
            label: string;
            amount: string;
            trialDays?: number;
        }>;
        baseUrl?: string;
        turnstileEnabled?: boolean;
        turnstileSiteKey?: string;
        transcriptionEnabled?: boolean;
    };
    playbackGoesLive: boolean;
    showListenersCount: boolean;
    systems: RdioScannerSystem[];
    tags: { [key: string]: { [key: number]: number[] } };
    tagsData: RdioScannerTagData[];
    time12hFormat: boolean;
    userSettings?: { [key: string]: any }; // User-specific settings (tag colors, etc.)
}

export interface RdioScannerEvent {
    alert?: any;
    auth?: boolean;
    categories?: RdioScannerCategory[];
    call?: RdioScannerCall;
    config?: RdioScannerConfig;
    connectionLimit?: number;
    error?: string;
    expired?: boolean;
    holdSys?: boolean;
    holdTg?: boolean;
    linked?: boolean;
    listeners?: number;
    livefeedMode?: RdioScannerLivefeedMode;
    map?: RdioScannerLivefeedMap;
    pause?: boolean;
    playbackList?: RdioScannerPlaybackList;
    playbackPending?: number;
    queue?: number;
    time?: number;
    tooMany?: boolean;
}

export interface RdioScannerGroupData {
    id: number;
    alert?: string;
    label?: string;
    led?: string;
}

export interface RdioScannerKeypadBeeps {
    [RdioScannerBeepStyle.Activate]: RdioScannerOscillatorData[];
    [RdioScannerBeepStyle.Deactivate]: RdioScannerOscillatorData[];
    [RdioScannerBeepStyle.Denied]: RdioScannerOscillatorData[];
}

export interface RdioScannerLivefeed {
    active: boolean;
    minutes: number | undefined;
    timer: Subscription | undefined;
}

export interface RdioScannerLivefeedMap {
    [key: number]: {
        [key: number]: RdioScannerLivefeed;
    };
}

export enum RdioScannerLivefeedMode {
    Offline = 'offline',
    Online = 'online',
    Playback = 'playback',
}

export interface RdioScannerOscillatorData {
    begin: number;
    end: number;
    frequency: number;
    type: OscillatorType;
}

export interface RdioScannerPlaybackList {
    count: number;
    hasMore?: boolean;
    dateStart: Date;
    dateStop: Date;
    options: RdioScannerSearchOptions;
    results: RdioScannerCall[];
}

export interface RdioScannerSearchOptions {
    date?: Date;
    group?: string;
    limit: number;
    offset: number;
    sort: number;
    system?: number;
    tag?: string;
    talkgroup?: number;
    unit?: number;
}

export interface RdioScannerSystem {
    id: number;
    systemRef?: number;
    alert?: string;
    label: string;
    led?: 'blue' | 'cyan' | 'green' | 'magenta' | 'orange' | 'red' | 'white' | 'yellow';
    order?: number;
    talkgroups: RdioScannerTalkgroup[];
    type?: string;
    units: RdioScannerUnit[];
}

export interface RdioScannerTagData {
    id: number;
    alert?: string;
    label?: string;
    led?: string;
}

export interface RdioScannerTalkgroup {
    alert?: string;
    frequency?: number;
    groups: string[];
    id: number;
    talkgroupRef?: number;
    label: string;
    led?: 'blue' | 'cyan' | 'green' | 'magenta' | 'orange' | 'red' | 'white' | 'yellow';
    name: string;
    toneDetectionEnabled?: boolean;
    toneSets?: RdioScannerToneSet[];
    tag: string;
    type?: string;
}

export interface RdioScannerUnit {
    id: number;
    label: string;
    unitRef: number;
    unitFrom: number;
    unitTo: number;
}

export { RdioScannerService } from './rdio-scanner.service';

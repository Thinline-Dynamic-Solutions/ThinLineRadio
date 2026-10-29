import { Config } from '../../admin.service';

export interface ConfigImportSection {
    key: string;
    label: string;
    description: string;
    count: number;
    detail?: string;
}

type RecordLike = { [key: string]: unknown };

const DEFAULT_OPTIONS = {
    defaultSystemDelay: 0,
    userRegistrationEnabled: false,
    publicRegistrationEnabled: false,
    publicRegistrationMode: 'both',
    stripePaywallEnabled: false,
    emailServiceEnabled: false,
    emailServiceType: 'smtp',
    emailServiceApiKey: '',
    emailServiceDomain: '',
    emailServiceTemplateId: '',
    emailSmtpHost: '',
    emailSmtpPort: 587,
    emailSmtpUsername: '',
    emailSmtpPassword: '',
    emailSmtpFromEmail: '',
    emailSmtpFromName: '',
    emailSmtpUseTLS: true,
    emailLogoFilename: '',
    emailLogoBorderRadius: '0px',
    stripePublishableKey: '',
    stripeSecretKey: '',
    stripeWebhookSecret: '',
    stripeBillingPortalConfigurationId: '',
    stripeGracePeriodDays: 0,
    baseUrl: '',
    adminLocalhostOnly: false,
    radioReferenceEnabled: false,
    radioReferenceUsername: '',
    radioReferencePassword: '',
    radioReferenceAPIKey: '',
};

function convertIdFields(items: RecordLike[] | undefined): void {
    items?.forEach((item) => {
        if (item['_id'] !== undefined && item['id'] === undefined) {
            item['id'] = item['_id'];
            delete item['_id'];
        }
    });
}

/** Normalize a raw JSON config export (v6/v7) for import. */
export function normalizeImportedConfig(raw: RecordLike, options?: { stripLegacyAccess?: boolean }): Config {
    const config: RecordLike = { ...raw };

    if (options?.stripLegacyAccess !== false && config['access'] !== undefined) {
        delete config['access'];
    }

    convertIdFields(Array.isArray(config['access']) ? config['access'] as RecordLike[] : undefined);
    convertIdFields(Array.isArray(config['users']) ? config['users'] as RecordLike[] : undefined);
    convertIdFields(Array.isArray(config['userGroups']) ? config['userGroups'] as RecordLike[] : undefined);

    if (config['apiKeys'] !== undefined && config['apikeys'] === undefined) {
        config['apikeys'] = config['apiKeys'];
        delete config['apiKeys'];
    }
    convertIdFields(Array.isArray(config['apikeys']) ? config['apikeys'] as RecordLike[] : undefined);

    if (config['dirWatch'] !== undefined && config['dirwatch'] === undefined) {
        config['dirwatch'] = config['dirWatch'];
        delete config['dirWatch'];
    }
    convertIdFields(Array.isArray(config['dirwatch']) ? config['dirwatch'] as RecordLike[] : undefined);

    if (Array.isArray(config['downstreams'])) {
        (config['downstreams'] as RecordLike[]).forEach((downstream) => {
            if (downstream['_id'] !== undefined && downstream['id'] === undefined) {
                downstream['id'] = downstream['_id'];
                delete downstream['_id'];
            }
            if (downstream['apiKey'] !== undefined && downstream['apikey'] === undefined) {
                downstream['apikey'] = downstream['apiKey'];
                delete downstream['apiKey'];
            }
        });
    }

    if (Array.isArray(config['groups'])) {
        (config['groups'] as RecordLike[]).forEach((group) => {
            if (group['_id'] !== undefined && group['id'] === undefined) {
                group['id'] = group['_id'];
                delete group['_id'];
            }
        });
        config['groups'] = (config['groups'] as { label: string }[]).sort((a, b) => a.label.localeCompare(b.label));
    }

    if (Array.isArray(config['tags'])) {
        (config['tags'] as RecordLike[]).forEach((tag) => {
            if (tag['_id'] !== undefined && tag['id'] === undefined) {
                tag['id'] = tag['_id'];
                delete tag['_id'];
            }
        });
        config['tags'] = (config['tags'] as { label: string }[]).sort((a, b) => a.label.localeCompare(b.label));
    }

    if (Array.isArray(config['systems'])) {
        (config['systems'] as RecordLike[]).forEach((system) => {
            if (system['_id'] !== undefined) {
                if (system['id'] !== undefined) {
                    system['systemRef'] = system['id'];
                }
                system['id'] = system['_id'];
                delete system['_id'];
            } else if (system['id'] !== undefined && system['systemRef'] === undefined) {
                system['systemRef'] = system['id'];
                delete system['id'];
            }

            if (system['sites'] === undefined) {
                system['sites'] = [];
            } else if (Array.isArray(system['sites'])) {
                (system['sites'] as RecordLike[]).forEach((site) => {
                    if (site['_id'] !== undefined && site['id'] === undefined) {
                        site['id'] = site['_id'];
                        delete site['_id'];
                    }
                });
            }

            if (Array.isArray(system['units'])) {
                (system['units'] as RecordLike[]).forEach((unit) => {
                    if (unit['id'] !== undefined && unit['unitRef'] === undefined && unit['_id'] === undefined) {
                        unit['unitRef'] = unit['id'];
                        delete unit['id'];
                    }
                    if (unit['_id'] !== undefined && unit['id'] === undefined) {
                        unit['id'] = unit['_id'];
                        delete unit['_id'];
                    }
                    if (unit['unitRef'] === undefined) unit['unitRef'] = null;
                    if (unit['unitFrom'] === undefined) unit['unitFrom'] = null;
                    if (unit['unitTo'] === undefined) unit['unitTo'] = null;
                });
            }

            const talkgroups = system['talkgroups'];
            if (Array.isArray(talkgroups)) {
                (talkgroups as RecordLike[]).forEach((talkgroup) => {
                    const groupId = talkgroup['groupId'];
                    if (groupId !== undefined) {
                        if (typeof groupId === 'number') {
                            talkgroup['groupIds'] = [groupId];
                        }
                        delete talkgroup['groupId'];
                    }
                    if (talkgroup['id'] !== undefined && talkgroup['talkgroupRef'] === undefined) {
                        talkgroup['talkgroupRef'] = talkgroup['id'];
                        delete talkgroup['id'];
                    }
                    if (talkgroup['_id'] !== undefined && talkgroup['id'] === undefined) {
                        talkgroup['id'] = talkgroup['_id'];
                        delete talkgroup['_id'];
                    }
                });
            }
        });
    }

    if (!config['options']) {
        config['options'] = {};
    }

    config['options'] = { ...DEFAULT_OPTIONS, ...(config['options'] as RecordLike) };

    const optionsRecord = config['options'] as RecordLike;
    for (const legacyKey of ['afsSystems', 'searchPatchedTalkgroups', 'tagsToggle']) {
        if (optionsRecord[legacyKey] !== undefined) {
            delete optionsRecord[legacyKey];
        }
    }

    return config as Config;
}

function countSystemsNested(config: RecordLike): { systems: number; talkgroups: number; units: number } {
    let talkgroups = 0;
    let units = 0;
    const systems = Array.isArray(config['systems']) ? config['systems'].length : 0;

    if (Array.isArray(config['systems'])) {
        for (const system of config['systems'] as RecordLike[]) {
            if (Array.isArray(system['talkgroups'])) talkgroups += (system['talkgroups'] as unknown[]).length;
            if (Array.isArray(system['units'])) units += (system['units'] as unknown[]).length;
        }
    }

    return { systems, talkgroups, units };
}

function arrayLen(value: unknown): number {
    return Array.isArray(value) ? value.length : 0;
}

/** Build selectable import sections from a normalized config file. */
export function listConfigImportSections(config: Config | RecordLike): ConfigImportSection[] {
    const record = config as RecordLike;
    const sections: ConfigImportSection[] = [];
    const nested = countSystemsNested(record);

    const addArray = (key: string, label: string, description: string, value: unknown) => {
        const count = arrayLen(value);
        if (count > 0) {
            sections.push({ key, label, description, count });
        }
    };

    addArray('groups', 'Groups', 'Talkgroup group labels referenced by systems', record['groups']);
    addArray('tags', 'Tags', 'Talkgroup tags referenced by systems', record['tags']);

    if (nested.systems > 0) {
        const parts = [`${nested.systems} systems`];
        if (nested.talkgroups > 0) parts.push(`${nested.talkgroups} talkgroups`);
        if (nested.units > 0) parts.push(`${nested.units} units`);
        sections.push({
            key: 'systems',
            label: 'Systems',
            description: 'Systems with nested talkgroups, units, and sites',
            count: nested.systems,
            detail: parts.join(', '),
        });
    }

    addArray('apikeys', 'API Keys', 'Inbound API keys for uploads', record['apikeys']);
    addArray('dirwatch', 'Dirwatch', 'Directory watch sources', record['dirwatch']);
    addArray('downstreams', 'Downstreams', 'Downstream relay targets', record['downstreams']);

    if (record['options'] && typeof record['options'] === 'object') {
        sections.push({
            key: 'options',
            label: 'Options',
            description: 'Server options, branding, transcription, email, and registration settings',
            count: 1,
        });
    }

    addArray('userGroups', 'User Groups', 'Permission groups for registered users', record['userGroups']);
    addArray('users', 'Users', 'Registered user accounts', record['users']);
    addArray('keywordLists', 'Keyword Lists', 'Alert keyword lists', record['keywordLists']);
    addArray('userAlertPreferences', 'Alert Preferences', 'Per-user alert notification preferences', record['userAlertPreferences']);
    addArray('deviceTokens', 'Push Device Tokens', 'Mobile push notification tokens', record['deviceTokens']);

    return sections;
}

/** Merge selected sections from an import file onto the current live config. */
export function mergeConfigSections(current: Config, imported: Config | RecordLike, selectedKeys: string[]): Config {
    const merged: RecordLike = JSON.parse(JSON.stringify(current));

    for (const key of selectedKeys) {
        if (key === 'options') {
            merged['options'] = {
                ...(merged['options'] ?? {}),
                ...((imported as RecordLike)['options'] ?? {}),
            };
            continue;
        }

        const importedRecord = imported as RecordLike;
        if (importedRecord[key] !== undefined) {
            merged[key] = JSON.parse(JSON.stringify(importedRecord[key]));
        }
    }

    return merged as Config;
}

export function decodeImportFileBuffer(buffer: ArrayBuffer | string | null): string {
    return decodeURIComponent(Array.prototype.map.call(buffer, (c: string) => {
        return '%' + ('00' + c.charCodeAt(0).toString(16)).slice(-2);
    }).join(''));
}

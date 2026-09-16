/*
 * Public scanner accent / primary color. Applied via --tlr-accent tokens.
 * Admin branding sets the site default; users can override in Settings.
 */

export const DEFAULT_UI_ACCENT = '#ff5b2e';

export interface UiAccentPreset {
    id: string;
    name: string;
    value: string;
}

export const UI_ACCENT_PRESETS: UiAccentPreset[] = [
    { id: 'ember', name: 'Ember', value: '#ff5b2e' },
    { id: 'amber', name: 'Amber', value: '#ffa63d' },
    { id: 'ice', name: 'Ice', value: '#5fd0ff' },
    { id: 'mint', name: 'Mint', value: '#38e0a4' },
    { id: 'white', name: 'White', value: '#e9edf4' },
];

export function normalizeUIAccentColor(raw: string | null | undefined): string {
    const s = (raw || '').trim().toLowerCase();
    if (!s) {
        return DEFAULT_UI_ACCENT;
    }
    const hex = s.startsWith('#') ? s : `#${s}`;
    if (/^#[0-9a-f]{3}$/.test(hex)) {
        return `#${hex[1]}${hex[1]}${hex[2]}${hex[2]}${hex[3]}${hex[3]}`;
    }
    if (/^#[0-9a-f]{6}$/.test(hex)) {
        return hex;
    }
    return DEFAULT_UI_ACCENT;
}

function hexToRgb(hex: string): string {
    const h = normalizeUIAccentColor(hex).slice(1);
    const n = parseInt(h, 16);
    return `${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}`;
}

const ACCENT_HOST_SELECTOR = '.scanner-shell, .thinline-skin';

function syncAccentVars(el: HTMLElement, color: string, rgb: string): void {
    el.style.setProperty('--tlr-accent', color);
    el.style.setProperty('--tlr-primary', color);
    el.style.setProperty('--primary-color', color);
    el.style.setProperty('--tlr-ember', color);
    el.style.setProperty('--tlr-accent-rgb', rgb);
}

export function applyUIAccentColor(raw: string | null | undefined): string {
    const color = normalizeUIAccentColor(raw);
    const rgb = hexToRgb(color);
    const root = document.documentElement;
    syncAccentVars(root, color, rgb);
    root.dataset['uiAccent'] = color;
    // .thinline-skin redefines --tlr-ember/--tlr-accent, which would otherwise
    // keep scanner chrome (tabs, slider, listeners) stuck on ember.
    document.querySelectorAll(ACCENT_HOST_SELECTOR).forEach((node) => {
        if (node instanceof HTMLElement) {
            syncAccentVars(node, color, rgb);
        }
    });
    return color;
}

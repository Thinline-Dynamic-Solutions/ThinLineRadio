/*
 * Shared app font list + apply helper.
 * Sets CSS custom properties on :root/body and syncs overlay/skin nodes so
 * nested scopes cannot reset fonts back to the build-time defaults.
 */

export interface AppFontOption {
    name: string;
    value: string;
    displayName: string;
}

export const APP_FONTS: AppFontOption[] = [
    { name: 'Roboto', value: 'Roboto, sans-serif', displayName: 'Roboto (Default)' },
    { name: 'Rajdhani', value: 'Rajdhani, sans-serif', displayName: 'Rajdhani (Modern Technical)' },
    { name: 'ShareTechMono', value: '"Share Tech Mono", monospace', displayName: 'Share Tech Mono (Terminal)' },
    { name: 'Audiowide', value: 'Audiowide, cursive', displayName: 'Audiowide (Digital Display)' },
];

function syncFontVars(el: HTMLElement, value: string): void {
    el.style.setProperty('--tlr-font-primary', value);
    el.style.setProperty('--tlr-font-numeric', value);
}

export function applyAppFont(fontName: string): void {
    const font = APP_FONTS.find(f => f.name === fontName) ?? APP_FONTS[0];
    const root = document.documentElement;
    const body = document.body;

    root.dataset['appFont'] = font.name;
    syncFontVars(root, font.value);
    syncFontVars(body, font.value);
    body.style.fontFamily = font.value;

    document.querySelectorAll('.thinline-skin, .cdk-overlay-container, app-root').forEach((el) => {
        if (el instanceof HTMLElement) {
            syncFontVars(el, font.value);
        }
    });

    if (fontName === 'Audiowide') {
        root.style.fontSize = '14.45px';
    } else {
        root.style.fontSize = '';
    }
}

/*
 * Site default + optional per-user override for the public scanner accent color.
 */

import { Injectable } from '@angular/core';
import { SettingsService } from './settings/settings.service';
import { applyUIAccentColor, DEFAULT_UI_ACCENT, normalizeUIAccentColor } from './app-accent.util';

@Injectable({ providedIn: 'root' })
export class AppAccentService {
    private siteColor = DEFAULT_UI_ACCENT;
    private userColor = '';

    constructor(private settingsService: SettingsService) {}

    init(): void {
        const initial = (window as { initialConfig?: { options?: { uiAccentColor?: string } } }).initialConfig;
        if (typeof initial?.options?.uiAccentColor === 'string') {
            this.siteColor = normalizeUIAccentColor(initial.options.uiAccentColor);
        }
        this.applyResolved();
        this.settingsService.getSettings().subscribe({
            next: (settings) => {
                const raw = settings?.uiAccentColor;
                this.userColor = typeof raw === 'string' ? raw.trim() : '';
                this.applyResolved();
            },
            error: () => {
                this.userColor = '';
                this.applyResolved();
            },
        });
    }

    setSiteColor(raw: string | null | undefined): void {
        this.siteColor = normalizeUIAccentColor(raw);
        this.applyResolved();
    }

    /** Empty string clears the user override and follows the site default. */
    setUserColor(raw: string | null | undefined): void {
        this.userColor = (raw || '').trim();
        this.applyResolved();
    }

    getSiteColor(): string {
        return this.siteColor;
    }

    getResolvedColor(): string {
        return this.userColor ? normalizeUIAccentColor(this.userColor) : this.siteColor;
    }

    hasUserOverride(): boolean {
        return !!this.userColor;
    }

    /** Re-apply after .thinline-skin / .scanner-shell mount. */
    reapply(): void {
        this.applyResolved();
    }

    private applyResolved(): void {
        applyUIAccentColor(this.getResolvedColor());
    }
}

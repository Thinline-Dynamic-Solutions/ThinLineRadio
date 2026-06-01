/*
 * Loads and applies the user's App Font setting app-wide.
 */

import { Injectable } from '@angular/core';
import { SettingsService } from './settings/settings.service';
import { APP_FONTS, applyAppFont } from './app-font.util';

@Injectable({ providedIn: 'root' })
export class AppFontService {
    private currentFont = 'Roboto';

    constructor(private settingsService: SettingsService) {}

    /** Apply Roboto immediately, then load saved preference from the server. */
    init(): void {
        applyAppFont(this.currentFont);
        this.settingsService.getSettings().subscribe({
            next: (settings) => {
                this.apply(settings?.appFont || 'Roboto');
            },
            error: () => {
                this.apply('Roboto');
            },
        });
    }

    apply(fontName: string): void {
        this.currentFont = APP_FONTS.some(f => f.name === fontName) ? fontName : 'Roboto';
        applyAppFont(this.currentFont);
    }

    getCurrentFont(): string {
        return this.currentFont;
    }
}

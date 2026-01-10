/*
 * *****************************************************************************
 * Copyright (C) 2025 Thinline Dynamic Solutions
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

import { Injectable, OnDestroy } from '@angular/core';
import { BehaviorSubject, Observable, Subscription } from 'rxjs';
import { SettingsService } from './settings/settings.service';
import { RdioScannerConfig, RdioScannerEvent } from './rdio-scanner';
import { RdioScannerService } from './rdio-scanner.service';

export interface TagColorConfig {
    [tagName: string]: string; // Tag name -> hex color
}

@Injectable()
export class TagColorService implements OnDestroy {
    // Predefined color options (excluding black)
    static readonly AVAILABLE_COLORS: Array<{name: string, value: string}> = [
        { name: 'White', value: '#ffffff' },
        { name: 'Red', value: '#ff1744' },
        { name: 'Orange', value: '#ff9100' },
        { name: 'Yellow', value: '#ffea00' },
        { name: 'Green', value: '#00e676' },
        { name: 'Cyan', value: '#00e5ff' },
        { name: 'Blue', value: '#2979ff' },
        { name: 'Magenta', value: '#d500f9' },
        { name: 'Gray', value: '#9e9e9e' },
    ];

    private readonly defaultColors: TagColorConfig = {
        '1': '#ff1744',      // Fire - Red
        '2': '#2979ff',      // Law - Blue
        '3': '#00e676',      // Public Works - Green
        '4': '#fff',         // EMS - White
        '5': '#ff9100',      // TAC - Orange
        '6': '#9e9e9e',      // Corrections - Gray
        'green': '#00e676',
        'blue': '#2979ff',
        'cyan': '#00e5ff',
        'magenta': '#d500f9',
        'orange': '#ff9100',
        'red': '#ff1744',
        'white': '#fff',
        'yellow': '#ffea00',
    };

    private tagColors$ = new BehaviorSubject<TagColorConfig>(this.defaultColors);
    private tagColors: TagColorConfig = { ...this.defaultColors };
    private availableTags: Set<string> = new Set();
    private configSubscription?: Subscription;
    private ledColorToTagLabel: Map<string, string> = new Map(); // Map LED colors to tag labels
    private configTagsData: any[] = []; // Store tag data from config for admin-set colors

    constructor(
        private settingsService: SettingsService,
        private rdioScannerService: RdioScannerService,
    ) {
        this.loadTagColors();
        
        // Subscribe to config events to get all available tags and user settings
        this.configSubscription = this.rdioScannerService.event.subscribe((event: RdioScannerEvent) => {
            if (event.config) {
                this.updateAvailableTags(event.config);
                
                // Load user settings from config if available
                if (event.config.userSettings && event.config.userSettings['tagColors']) {
                    this.tagColors = { ...this.defaultColors, ...event.config.userSettings['tagColors'] };
                    this.tagColors$.next({ ...this.tagColors });
                }
            }
        });
    }

    ngOnDestroy(): void {
        if (this.configSubscription) {
            this.configSubscription.unsubscribe();
        }
    }

    getAvailableTags(): Set<string> {
        return new Set(this.availableTags);
    }

    getAllTags(): string[] {
        return Array.from(this.availableTags).sort();
    }

    private updateAvailableTags(config: RdioScannerConfig): void {
        const newTags = new Set<string>();
        const tagIdToLabel = new Map<string, string>(); // Map tag IDs to labels for better display
        
        // First, process tagsData to get labels (this is the primary source)
        if (config.tagsData && Array.isArray(config.tagsData)) {
            config.tagsData.forEach(tagData => {
                const tagLabel = tagData.label;
                
                // Only add labels (not numeric IDs) - labels are the actual tag names
                if (tagLabel && tagLabel.trim() !== '') {
                    // Don't add numeric-only labels (filter out pure numbers like "1", "2", etc.)
                    if (!/^\d+$/.test(tagLabel.trim())) {
                        newTags.add(tagLabel.trim());
                    }
                }
                
                // Store LED color mapping for lookup, but don't add LED colors to the tag list
                if (tagData.led && tagLabel && tagLabel.trim() !== '' && !/^\d+$/.test(tagLabel.trim())) {
                    tagIdToLabel.set(tagData.led.toLowerCase(), tagLabel.trim());
                    // Store mapping from LED color to tag label for color lookup
                    this.ledColorToTagLabel.set(tagData.led.toLowerCase(), tagLabel.trim());
                }
            });
        }
        
        // Get tags from config.tags (object with tag names/labels as keys)
        if (config.tags) {
            Object.keys(config.tags).forEach(tagKey => {
                // Prefer label from tagsData if available
                const label = tagIdToLabel.get(tagKey) || tagKey;
                
                // Only add non-numeric labels
                if (label && !/^\d+$/.test(label.trim())) {
                    newTags.add(label.trim());
                }
            });
        }
        
        // Don't add default numeric tags or default color names - only show actual tag labels from config
        
        this.availableTags = newTags;
        
        // Ensure all tags have default colors if not already set
        const updatedColors = { ...this.tagColors };
        let hasChanges = false;
        
        this.availableTags.forEach(tag => {
            const tagKey = tag.toLowerCase();
            if (!updatedColors[tagKey]) {
                // Check for default color first (might match LED color or known tag)
                if (this.defaultColors[tagKey]) {
                    updatedColors[tagKey] = this.defaultColors[tagKey];
                } else {
                    // New tag without a default - use white as fallback
                    updatedColors[tagKey] = '#fff';
                }
                hasChanges = true;
            }
        });
        
        if (hasChanges) {
            this.tagColors = updatedColors;
            this.tagColors$.next({ ...this.tagColors });
        }
    }

    getTagColors(): Observable<TagColorConfig> {
        return this.tagColors$.asObservable();
    }

    getTagColor(tag: string | number | null | undefined): string {
        if (!tag) return '#fff';
        
        const tagKey = tag.toString().toLowerCase();
        
        // First, check if this is an LED color name (like "green", "red", etc.)
        // and if so, look up the corresponding tag label
        const tagLabel = this.ledColorToTagLabel.get(tagKey);
        if (tagLabel) {
            const labelKey = tagLabel.toLowerCase();
            // Check if we have a custom color for the tag label (user setting - highest priority)
            if (this.tagColors[labelKey]) {
                return this.tagColors[labelKey];
            }
            // Also check the LED color key directly (in case color was set by LED name)
            if (this.tagColors[tagKey]) {
                return this.tagColors[tagKey];
            }
        }
        
        // Check if we have a custom color for this tag directly (by label or LED name) - user setting
        if (this.tagColors[tagKey]) {
            return this.tagColors[tagKey];
        }

        // Check admin-set default colors from config (by tag ID or label)
        const tagNum = typeof tag === 'number' ? tag : parseInt(tag.toString());
        if (!isNaN(tagNum) && this.configTagsData.length > 0) {
            const configTag = this.configTagsData.find((t: any) => t.id === tagNum);
            if (configTag?.color) {
                return configTag.color;
            }
        }
        
        // Also check by label for non-numeric tags
        if (this.configTagsData.length > 0) {
            const configTag = this.configTagsData.find((t: any) => 
                t.label && t.label.toLowerCase() === tagKey
            );
            if (configTag?.color) {
                return configTag.color;
            }
        }

        // Try to parse as number and map to hardcoded default
        if (!isNaN(tagNum) && this.defaultColors[tagNum.toString()]) {
            return this.defaultColors[tagNum.toString()];
        }

        // Try name-based matching for common tags
        const lowerTag = tagKey;
        if (lowerTag.includes('fire')) return this.tagColors['fire'] || this.defaultColors['red'] || '#ff1744';
        if (lowerTag.includes('law') || lowerTag.includes('police')) return this.tagColors[lowerTag] || this.defaultColors['blue'] || '#2979ff';
        if (lowerTag.includes('public works') || lowerTag.includes('works')) return this.tagColors[lowerTag] || this.defaultColors['green'] || '#00e676';
        if (lowerTag.includes('ems') || lowerTag.includes('medical')) return this.tagColors[lowerTag] || this.defaultColors['white'] || '#fff';
        if (lowerTag.includes('tac')) return this.tagColors[lowerTag] || this.defaultColors['orange'] || '#ff9100';
        if (lowerTag.includes('jail') || lowerTag.includes('correction')) return this.tagColors[lowerTag] || this.defaultColors['gray'] || '#9e9e9e';

        // Check if it's a known LED color name
        if (this.defaultColors[tagKey]) {
            return this.defaultColors[tagKey];
        }

        // Default to white if nothing matches
        return '#fff';
    }

    setTagColor(tag: string, color: string): void {
        const tagKey = tag.toLowerCase();
        this.tagColors[tagKey] = color;
        
        // Also store the color by LED color name if this tag has an associated LED color
        // This ensures colors work when tags are referenced by LED color name (e.g., call.tagData.led)
        for (const [ledColor, tagLabel] of this.ledColorToTagLabel.entries()) {
            if (tagLabel.toLowerCase() === tagKey) {
                this.tagColors[ledColor] = color;
            }
        }
        
        this.tagColors$.next({ ...this.tagColors });
        this.saveTagColors();
    }

    resetTagColor(tag: string): void {
        const tagKey = tag.toLowerCase();
        delete this.tagColors[tagKey];
        this.tagColors$.next({ ...this.tagColors });
        this.saveTagColors();
    }

    resetAllColors(): void {
        this.tagColors = { ...this.defaultColors };
        this.tagColors$.next({ ...this.tagColors });
        this.saveTagColors();
    }

    getAllTagColors(): TagColorConfig {
        return { ...this.tagColors };
    }

    getAvailableColors(): Array<{name: string, value: string}> {
        return TagColorService.AVAILABLE_COLORS;
    }

    private loadTagColors(): void {
        // First, try to load from config (comes with login)
        const currentConfig = this.rdioScannerService.getConfig();
        if (currentConfig && currentConfig.userSettings && currentConfig.userSettings['tagColors']) {
            // Load from config userSettings (from server after login)
            this.tagColors = { ...this.defaultColors, ...currentConfig.userSettings['tagColors'] };
            if (currentConfig) {
                this.updateAvailableTags(currentConfig);
            }
            this.tagColors$.next({ ...this.tagColors });
            return;
        }

        // Fallback: try to load from API (for backwards compatibility or initial load)
        this.settingsService.getSettings().subscribe({
            next: (settings) => {
                if (settings && settings.tagColors) {
                    // Merge custom colors with defaults
                    // Start with defaults, then apply saved colors
                    this.tagColors = { ...this.defaultColors, ...settings.tagColors };
                } else {
                    // Use defaults if no settings
                    this.tagColors = { ...this.defaultColors };
                }
                
                // Also get tags from current config if available
                const currentConfig = this.rdioScannerService.getConfig();
                if (currentConfig) {
                    this.updateAvailableTags(currentConfig);
                }
                
                this.tagColors$.next({ ...this.tagColors });
            },
            error: (error) => {
                console.error('Error loading tag colors from settings:', error);
                // Use defaults on error
                this.tagColors = { ...this.defaultColors };
                
                // Still try to get tags from current config
                const currentConfig = this.rdioScannerService.getConfig();
                if (currentConfig) {
                    this.updateAvailableTags(currentConfig);
                }
                
                this.tagColors$.next({ ...this.tagColors });
            },
        });
    }

    private saveTagColors(): void {
        this.settingsService.getSettings().subscribe({
            next: (currentSettings) => {
                const updatedSettings = {
                    ...currentSettings,
                    tagColors: this.tagColors,
                };
                this.settingsService.saveSettings(updatedSettings).subscribe({
                    next: () => {
                        console.log('Tag colors saved to settings');
                    },
                    error: (error) => {
                        console.error('Error saving tag colors:', error);
                    },
                });
            },
            error: (error) => {
                console.error('Error loading current settings for save:', error);
                // Save just tag colors if we can't load current settings
                this.settingsService.saveSettings({ tagColors: this.tagColors }).subscribe({
                    error: (saveError) => {
                        console.error('Error saving tag colors:', saveError);
                    },
                });
            },
        });
    }
}


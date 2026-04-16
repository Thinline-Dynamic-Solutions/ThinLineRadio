/*
 * *****************************************************************************
 * Copyright (C) 2026 Carter Carling <carter@cartercarling.com>
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

import { Component, OnInit } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { MatSnackBar } from '@angular/material/snack-bar';
import { firstValueFrom } from 'rxjs';
import { RdioScannerAdminService } from '../../admin.service';

export interface FuzzyWord {
    word: string;
    maxDistance: number;
    aliases?: string[];
    reject?: string[];
}

export interface ChannelShorthand {
    label: string;
    dispatch: string;
    separator?: string;
}

export interface TranscriptConfig {
    unitTypes: FuzzyWord[];
    unitPrefixes: FuzzyWord[];
    dispatchNames: FuzzyWord[];
    channelSeparators: FuzzyWord[];
    channelShorthands: ChannelShorthand[];
    corrections: FuzzyWord[];
}

type FuzzyListKey = 'unitTypes' | 'unitPrefixes' | 'dispatchNames' | 'channelSeparators' | 'corrections';

const emptyConfig = (): TranscriptConfig => ({
    unitTypes: [],
    unitPrefixes: [],
    dispatchNames: [],
    channelSeparators: [],
    channelShorthands: [],
    corrections: [],
});

@Component({
    selector: 'rdio-scanner-admin-transcript-parser',
    templateUrl: './transcript-parser.component.html',
    styleUrls: ['./transcript-parser.component.scss'],
})
export class RdioScannerAdminTranscriptParserComponent implements OnInit {
    config: TranscriptConfig = emptyConfig();
    loading = false;
    saving = false;
    showDocs = false;

    // Fuzzy word editing state
    editingFuzzy: { listKey: FuzzyListKey; index: number } | null = null;
    editingFuzzyData: FuzzyWord | null = null;
    newAliasText = '';
    newRejectText = '';

    // Channel shorthand editing state
    editingShorthand: number | null = null;
    editingShorthandData: ChannelShorthand | null = null;

    constructor(
        private adminService: RdioScannerAdminService,
        private http: HttpClient,
        private snackBar: MatSnackBar,
    ) {}

    ngOnInit(): void {
        this.load();
    }

    async load(): Promise<void> {
        this.loading = true;
        try {
            const res = await firstValueFrom(
                this.http.get<TranscriptConfig>(
                    `${window.location.origin}/api/admin/transcript-parser`,
                    { headers: this.adminService.getAuthHeaders() },
                ),
            );
            this.config = {
                unitTypes: res.unitTypes || [],
                unitPrefixes: res.unitPrefixes || [],
                dispatchNames: res.dispatchNames || [],
                channelSeparators: res.channelSeparators || [],
                channelShorthands: res.channelShorthands || [],
                corrections: res.corrections || [],
            };
        } catch {
            this.snackBar.open('Failed to load transcript parser config', 'Dismiss', { duration: 4000 });
        } finally {
            this.loading = false;
        }
    }

    async save(): Promise<void> {
        this.saving = true;
        try {
            await firstValueFrom(
                this.http.put(
                    `${window.location.origin}/api/admin/transcript-parser`,
                    this.config,
                    { headers: this.adminService.getAuthHeaders() },
                ),
            );
            this.snackBar.open('Transcript parser config saved', 'Dismiss', { duration: 3000 });
        } catch {
            this.snackBar.open('Failed to save transcript parser config', 'Dismiss', { duration: 4000 });
        } finally {
            this.saving = false;
        }
    }

    reset(): void {
        this.cancelFuzzy();
        this.cancelShorthand();
        this.load();
    }

    // ─── FuzzyWord list helpers ───────────────────────────────────────────────

    addFuzzy(listKey: FuzzyListKey): void {
        const list = this.config[listKey] as FuzzyWord[];
        list.push({ word: '', maxDistance: 0, aliases: [], reject: [] });
        this.editFuzzy(listKey, list.length - 1);
    }

    editFuzzy(listKey: FuzzyListKey, index: number): void {
        this.cancelShorthand();
        const original = (this.config[listKey] as FuzzyWord[])[index];
        this.editingFuzzyData = {
            word: original.word,
            maxDistance: original.maxDistance,
            aliases: [...(original.aliases || [])],
            reject: [...(original.reject || [])],
        };
        this.editingFuzzy = { listKey, index };
        this.newAliasText = '';
        this.newRejectText = '';
    }

    saveFuzzy(): void {
        if (!this.editingFuzzy || !this.editingFuzzyData) return;
        const { listKey, index } = this.editingFuzzy;
        const list = this.config[listKey] as FuzzyWord[];
        list[index] = {
            word: this.editingFuzzyData.word.toUpperCase().trim(),
            maxDistance: Number(this.editingFuzzyData.maxDistance) || 0,
            aliases: (this.editingFuzzyData.aliases || []).length > 0 ? this.editingFuzzyData.aliases : undefined,
            reject: (this.editingFuzzyData.reject || []).length > 0 ? this.editingFuzzyData.reject : undefined,
        };
        this.cancelFuzzy();
    }

    cancelFuzzy(): void {
        // If the row being cancelled is a newly added empty entry, remove it
        if (this.editingFuzzy && this.editingFuzzyData) {
            const { listKey, index } = this.editingFuzzy;
            const list = this.config[listKey] as FuzzyWord[];
            if (list[index].word === '') {
                list.splice(index, 1);
            }
        }
        this.editingFuzzy = null;
        this.editingFuzzyData = null;
        this.newAliasText = '';
        this.newRejectText = '';
    }

    deleteFuzzy(listKey: FuzzyListKey, index: number): void {
        if (this.editingFuzzy?.listKey === listKey) {
            if (this.editingFuzzy.index === index) {
                this.editingFuzzy = null;
                this.editingFuzzyData = null;
            } else if (this.editingFuzzy.index > index) {
                this.editingFuzzy = { ...this.editingFuzzy, index: this.editingFuzzy.index - 1 };
            }
        }
        (this.config[listKey] as FuzzyWord[]).splice(index, 1);
    }

    isEditingFuzzy(listKey: FuzzyListKey, index: number): boolean {
        return this.editingFuzzy?.listKey === listKey && this.editingFuzzy?.index === index;
    }

    // ─── Alias chip helpers ───────────────────────────────────────────────────

    addAlias(): void {
        const text = this.newAliasText.toUpperCase().trim();
        if (!text || !this.editingFuzzyData) return;
        const aliases = this.editingFuzzyData.aliases || [];
        if (!aliases.includes(text)) {
            aliases.push(text);
            this.editingFuzzyData.aliases = aliases;
        }
        this.newAliasText = '';
    }

    removeAlias(index: number): void {
        this.editingFuzzyData?.aliases?.splice(index, 1);
    }

    // ─── Reject chip helpers ──────────────────────────────────────────────────

    addReject(): void {
        const text = this.newRejectText.toUpperCase().trim();
        if (!text || !this.editingFuzzyData) return;
        const reject = this.editingFuzzyData.reject || [];
        if (!reject.includes(text)) {
            reject.push(text);
            this.editingFuzzyData.reject = reject;
        }
        this.newRejectText = '';
    }

    removeReject(index: number): void {
        this.editingFuzzyData?.reject?.splice(index, 1);
    }

    // ─── ChannelShorthand helpers ─────────────────────────────────────────────

    addShorthand(): void {
        this.config.channelShorthands.push({ label: '', dispatch: '', separator: '' });
        this.editShorthand(this.config.channelShorthands.length - 1);
    }

    editShorthand(index: number): void {
        this.cancelFuzzy();
        const original = this.config.channelShorthands[index];
        this.editingShorthandData = { ...original };
        this.editingShorthand = index;
    }

    saveShorthand(): void {
        if (this.editingShorthand === null || !this.editingShorthandData) return;
        this.config.channelShorthands[this.editingShorthand] = {
            label: this.editingShorthandData.label.toUpperCase().trim(),
            dispatch: this.editingShorthandData.dispatch.toUpperCase().trim(),
            separator: this.editingShorthandData.separator?.toUpperCase().trim() || undefined,
        };
        this.cancelShorthand();
    }

    cancelShorthand(): void {
        if (this.editingShorthand !== null && this.editingShorthandData) {
            const sh = this.config.channelShorthands[this.editingShorthand];
            if (sh && sh.label === '') {
                this.config.channelShorthands.splice(this.editingShorthand, 1);
            }
        }
        this.editingShorthand = null;
        this.editingShorthandData = null;
    }

    deleteShorthand(index: number): void {
        if (this.editingShorthand === index) {
            this.editingShorthand = null;
            this.editingShorthandData = null;
        } else if (this.editingShorthand !== null && this.editingShorthand > index) {
            this.editingShorthand -= 1;
        }
        this.config.channelShorthands.splice(index, 1);
    }

    // ─── Template helpers ─────────────────────────────────────────────────────

    getFuzzyList(listKey: FuzzyListKey): FuzzyWord[] {
        return this.config[listKey] as FuzzyWord[];
    }

    aliasPreview(fw: FuzzyWord): string {
        if (!fw.aliases || fw.aliases.length === 0) return '—';
        return fw.aliases.slice(0, 3).join(', ') + (fw.aliases.length > 3 ? ', …' : '');
    }

    onAliasKeydown(event: KeyboardEvent): void {
        if (event.key === 'Enter') {
            event.preventDefault();
            this.addAlias();
        }
    }

    onRejectKeydown(event: KeyboardEvent): void {
        if (event.key === 'Enter') {
            event.preventDefault();
            this.addReject();
        }
    }
}

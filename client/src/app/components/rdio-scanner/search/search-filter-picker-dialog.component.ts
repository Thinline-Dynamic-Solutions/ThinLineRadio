/*
 * *****************************************************************************
 * Copyright (C) 2025-2026 Thinline Dynamic Solutions
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 * ****************************************************************************
 */

import { ChangeDetectionStrategy, Component, Inject } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';

export interface SearchFilterPickerItem {
    index: number;
    label: string;
}

export interface SearchFilterPickerData {
    title: string;
    mode: 'single' | 'multi';
    items: SearchFilterPickerItem[];
    /** Selected item indexes. Empty in multi mode means "all". */
    selectedIndexes: number[];
    allLabel: string;
    /** Index used for the "all" option in single mode (usually -1). */
    allIndex?: number;
}

export interface SearchFilterPickerResult {
    selectedIndexes: number[];
}

@Component({
    selector: 'rdio-scanner-search-filter-picker-dialog',
    templateUrl: './search-filter-picker-dialog.component.html',
    styleUrls: ['./search-filter-picker-dialog.component.scss'],
    changeDetection: ChangeDetectionStrategy.Default,
    standalone: false,
})
export class SearchFilterPickerDialogComponent {
    query = '';
    selected = new Set<number>();
    readonly allIndex: number;
    readonly mode: 'single' | 'multi';
    readonly title: string;
    readonly allLabel: string;
    readonly items: SearchFilterPickerItem[];

    constructor(
        private dialogRef: MatDialogRef<SearchFilterPickerDialogComponent, SearchFilterPickerResult | undefined>,
        @Inject(MAT_DIALOG_DATA) data: SearchFilterPickerData,
    ) {
        this.title = data.title;
        this.mode = data.mode;
        this.items = data.items || [];
        this.allLabel = data.allLabel;
        this.allIndex = data.allIndex ?? -1;
        for (const idx of data.selectedIndexes || []) {
            this.selected.add(idx);
        }
    }

    get filteredItems(): SearchFilterPickerItem[] {
        const q = this.query.trim().toLowerCase();
        if (!q) {
            return this.items;
        }
        return this.items.filter((item) => item.label.toLowerCase().includes(q));
    }

    get isAllSelected(): boolean {
        return this.mode === 'multi' ? this.selected.size === 0 : this.selected.has(this.allIndex);
    }

    get selectedCountLabel(): string {
        if (this.mode !== 'multi') {
            return '';
        }
        if (this.selected.size === 0) {
            return this.allLabel;
        }
        if (this.selected.size === 1) {
            const only = [...this.selected][0];
            return this.items.find((item) => item.index === only)?.label || '1 selected';
        }
        return `${this.selected.size} selected`;
    }

    selectAll(): void {
        if (this.mode === 'single') {
            this.dialogRef.close({ selectedIndexes: [this.allIndex] });
            return;
        }
        this.selected.clear();
    }

    pickSingle(index: number): void {
        this.dialogRef.close({ selectedIndexes: [index] });
    }

    toggleMulti(index: number): void {
        if (this.selected.has(index)) {
            this.selected.delete(index);
        } else {
            this.selected.add(index);
        }
    }

    isSelected(index: number): boolean {
        return this.selected.has(index);
    }

    apply(): void {
        this.dialogRef.close({
            selectedIndexes: [...this.selected].sort((a, b) => a - b),
        });
    }

    cancel(): void {
        this.dialogRef.close(undefined);
    }
}

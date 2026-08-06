import { Component, Inject, ChangeDetectionStrategy } from '@angular/core';
import { MAT_DIALOG_DATA } from '@angular/material/dialog';
import { RdioScannerKeywordList } from '../../rdio-scanner';

export interface KeywordListInfoDialogData {
    list: RdioScannerKeywordList;
}

@Component({
    selector: 'rdio-scanner-keyword-list-info-dialog',
    templateUrl: './keyword-list-info-dialog.component.html',
    styleUrls: ['./keyword-list-info-dialog.component.scss'],
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class KeywordListInfoDialogComponent {
    keywords: string[];

    constructor(@Inject(MAT_DIALOG_DATA) public data: KeywordListInfoDialogData) {
        this.keywords = (data.list.keywords || []).filter(k => !!k?.trim());
    }
}

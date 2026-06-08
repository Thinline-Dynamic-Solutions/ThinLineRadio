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

import { Component, ElementRef, ViewChild } from '@angular/core';
import { MatSnackBar } from '@angular/material/snack-bar';
import { CopilotMessage, RdioScannerAdminService } from '../admin.service';

@Component({
    selector: 'rdio-scanner-admin-assistant',
    styleUrls: ['./assistant.component.scss'],
    templateUrl: './assistant.component.html',
})
export class RdioScannerAdminAssistantComponent {
    messages: CopilotMessage[] = [];
    input = '';
    loading = false;
    error: string | null = null;

    readonly suggestions = [
        'What should I check first on this server?',
        'Show recent error logs from the last 24 hours',
        'Are there any active system health alerts?',
        'Audit talkgroup tags for mismatches',
        'How do I configure Stripe webhooks?',
    ];

    @ViewChild('thread') private threadEl?: ElementRef<HTMLDivElement>;
    @ViewChild('inputBox') private inputEl?: ElementRef<HTMLTextAreaElement>;

    constructor(
        private adminService: RdioScannerAdminService,
        private snackBar: MatSnackBar,
    ) {}

    useSuggestion(text: string): void {
        this.input = text;
        this.inputEl?.nativeElement.focus();
    }

    clearChat(): void {
        this.messages = [];
        this.error = null;
    }

    async send(): Promise<void> {
        const text = this.input.trim();
        if (!text || this.loading) {
            return;
        }

        this.error = null;
        this.messages = [...this.messages, { role: 'user', content: text }];
        this.input = '';
        this.loading = true;
        this.scrollToBottom();

        try {
            const res = await this.adminService.copilotChat(this.messages);
            if (res.message?.content) {
                this.messages = [...this.messages, res.message];
            }
            if (res.toolsUsed?.length) {
                this.snackBar.open(`Used tools: ${res.toolsUsed.join(', ')}`, 'OK', { duration: 4000 });
            }
        } catch (e: unknown) {
            const msg = e instanceof Error ? e.message : 'Assistant request failed';
            this.error = msg;
            this.snackBar.open(msg, 'Dismiss', { duration: 6000 });
        } finally {
            this.loading = false;
            this.scrollToBottom();
        }
    }

    onKeydown(event: KeyboardEvent): void {
        if (event.key === 'Enter' && !event.shiftKey) {
            event.preventDefault();
            void this.send();
        }
    }

    private scrollToBottom(): void {
        setTimeout(() => {
            const el = this.threadEl?.nativeElement;
            if (el) {
                el.scrollTop = el.scrollHeight;
            }
        }, 50);
    }
}

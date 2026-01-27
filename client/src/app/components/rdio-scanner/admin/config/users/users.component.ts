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

import { Component, Input, OnInit, OnDestroy, OnChanges, SimpleChanges, ChangeDetectorRef } from '@angular/core';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatDialog } from '@angular/material/dialog';
import { FormBuilder, FormGroup, FormArray, Validators } from '@angular/forms';
import { HttpClient } from '@angular/common/http';
import { PageEvent } from '@angular/material/paginator';
import { RdioScannerAdminService, System } from '../../admin.service';
import { InviteUserDialogComponent, InvitationResultsDialogComponent, CreateUserDialogComponent, ResetPasswordDialogComponent } from './invite-user-dialog.component';
import { TransferUserDialogComponent } from './transfer-user-dialog.component';
import { RdioScannerAdminSystemsSelectComponent } from '../systems/select/select.component';

export interface User {
    id: number;
    email: string;
    verified: boolean;
    createdAt: string;
    lastLogin: string;
    firstName: string;
    lastName: string;
    zipCode: string;
    systems: string;
    delay: number;
    systemDelays: string;
    talkgroupDelays: string;
    pin: string;
    pinExpiresAt: number;
    pinExpired?: boolean;
    connectionLimit: number;
    effectiveConnectionLimit?: number;
    userGroupId?: number;
    isGroupAdmin?: boolean;
    systemAdmin?: boolean;
    stripeCustomerId: string;
    stripeSubscriptionId: string;
    subscriptionStatus: string;
}

@Component({
    selector: 'rdio-scanner-admin-users',
    templateUrl: './users.component.html',
    styleUrls: ['./users.component.scss']
})
export class RdioScannerAdminUsersComponent implements OnInit, OnDestroy, OnChanges {
    @Input() userRegistrationEnabled = false;
    @Input() form?: FormArray;

    users: User[] = [];
    loading = false;
    error: string | null = null;
    
    // Search and pagination
    searchText = '';
    filteredUsers: User[] = [];
    paginatedUsers: User[] = [];
    pageSize = 25;
    pageIndex = 0;
    
    // Editing functionality
    editingUser: User | null = null;
    editForm: FormGroup;
    saving = false;
    regeneratePinRequested = false;

    availableGroups: any[] = [];
    inviting = false;
    systems: System[] = [];
    configGroups: any[] = [];
    configTags: any[] = [];
    systemDelayEntries: Array<{systemId: number, delay: number}> = [];
    talkgroupDelayEntries: Array<{systemId: number, talkgroupId: number, delay: number}> = [];

    constructor(
        private adminService: RdioScannerAdminService,
        private matSnackBar: MatSnackBar,
        private matDialog: MatDialog,
        private cdr: ChangeDetectorRef,
        private fb: FormBuilder,
        private http: HttpClient
    ) {
        this.editForm = this.fb.group({
            email: ['', [Validators.required, Validators.email]],
            firstName: ['', [Validators.required]],
            lastName: ['', [Validators.required]],
            zipCode: ['', [Validators.required, Validators.pattern(/^\d{5}(-\d{4})?$/)]],
            verified: [false],
            systems: ['*'], // System and talkgroup access - '*' for all or JSON array
            delay: [0, [Validators.min(0)]],
            pin: [''],
            pinExpiresAt: [''], // datetime-local string format
            connectionLimit: [0, [Validators.min(0)]],
            systemDelays: [''],
            talkgroupDelays: [''],
            userGroupId: [0],
            isGroupAdmin: [false],
            systemAdmin: [false],
            stripeCustomerId: [''],
            stripeSubscriptionId: [''],
            subscriptionStatus: ['']
        });
    }

    ngOnInit(): void {
        this.loadUsers();
        this.loadGroups();
        this.loadSystems();
    }

    ngOnChanges(changes: SimpleChanges): void {
        if (changes['form'] && this.form) {
            // When form data is provided (e.g., from import for review), use it
            this.updateUsersFromForm();
        }
    }

    updateUsersFromForm(): void {
        if (this.form) {
            this.users = this.form.value.map((user: any) => ({
                id: user.id || 0,
                email: user.email || '',
                firstName: user.firstName || '',
                lastName: user.lastName || '',
                zipCode: user.zipCode || '',
                verified: user.verified || false,
                createdAt: user.createdAt || '',
                lastLogin: user.lastLogin || '',
                systems: user.systems || '',
                delay: user.delay || 0,
                systemDelays: user.systemDelays || '',
                talkgroupDelays: user.talkgroupDelays || '',
                settings: user.settings || '',
                pin: user.pin || '',
                pinExpiresAt: user.pinExpiresAt || 0,
                connectionLimit: user.connectionLimit || 0,
                userGroupId: user.userGroupId || 0,
                isGroupAdmin: user.isGroupAdmin || false,
                systemAdmin: user.systemAdmin || false,
                stripeCustomerId: user.stripeCustomerId || '',
                stripeSubscriptionId: user.stripeSubscriptionId || '',
                subscriptionStatus: user.subscriptionStatus || '',
                accountExpiresAt: user.accountExpiresAt || 0,
                pinExpired: false // Will be calculated if needed
            }));
            this.applyFilter();
            this.cdr.detectChanges();
        }
    }

    async loadSystems(): Promise<void> {
        const config = await this.adminService.getConfig();
        this.systems = config.systems || [];
        
        // Also store groups and tags for the system selection dialog
        this.configGroups = config.groups || [];
        this.configTags = config.tags || [];
    }

    getTalkgroupsForSystem(systemId: number): any[] {
        const system = this.systems.find(s => s.id === systemId);
        return system?.talkgroups || [];
    }

    addSystemDelay(): void {
        this.systemDelayEntries.push({ systemId: 0, delay: 0 });
    }

    removeSystemDelay(index: number): void {
        this.systemDelayEntries.splice(index, 1);
    }

    addTalkgroupDelay(): void {
        this.talkgroupDelayEntries.push({ systemId: 0, talkgroupId: 0, delay: 0 });
    }

    removeTalkgroupDelay(index: number): void {
        this.talkgroupDelayEntries.splice(index, 1);
    }

    loadGroups(): void {
        this.http.get('/api/admin/groups', { headers: this.adminService.getAuthHeaders() }).subscribe({
            next: (response: any) => {
                this.availableGroups = response.groups || [];
            },
            error: (error) => {
                console.error('Failed to load groups', error);
            }
        });
    }

    openInviteDialog(): void {
        const dialogRef = this.matDialog.open(InviteUserDialogComponent, {
            width: '600px',
            data: { groups: this.availableGroups }
        });

        dialogRef.afterClosed().subscribe(result => {
            if (result && result.emails && result.emails.length > 0) {
                this.inviteUsers(result.emails, result.groupId);
            }
        });
    }

    inviteUsers(emails: string[], groupId: number): void {
        if (emails.length === 0) {
            return;
        }

        this.inviting = true;
        const results: Array<{ email: string; success: boolean; message: string }> = [];
        let completed = 0;

        // Send invitations sequentially to avoid overwhelming the server
        const sendNext = (index: number) => {
            if (index >= emails.length) {
                this.inviting = false;
                this.showInvitationResults(results);
                return;
            }

            const email = emails[index];
            this.http.post('/api/admin/invitations', { email, groupId }, { headers: this.adminService.getAuthHeaders() }).subscribe({
                next: () => {
                    results.push({ email, success: true, message: 'Invitation sent successfully' });
                    completed++;
                    sendNext(index + 1);
                },
                error: (error) => {
                    const message = error.error?.message || error.error?.error || 'Failed to send invitation';
                    results.push({ email, success: false, message });
                    completed++;
                    sendNext(index + 1);
                }
            });
        };

        sendNext(0);
    }

    showInvitationResults(results: Array<{ email: string; success: boolean; message: string }>): void {
        const successCount = results.filter(r => r.success).length;
        const failureCount = results.filter(r => !r.success).length;

        if (failureCount === 0) {
            // All successful
            this.matSnackBar.open(
                `Successfully sent ${successCount} invitation${successCount !== 1 ? 's' : ''}`,
                'Close',
                { duration: 5000, panelClass: ['success-snackbar'] }
            );
        } else if (successCount === 0) {
            // All failed
            this.matSnackBar.open(
                `Failed to send all ${failureCount} invitation${failureCount !== 1 ? 's' : ''}`,
                'Close',
                { duration: 5000, panelClass: ['error-snackbar'] }
            );
        } else {
            // Mixed results - show detailed dialog
            const dialogRef = this.matDialog.open(InvitationResultsDialogComponent, {
                width: '600px',
                data: { results, successCount, failureCount }
            });
        }
    }

    inviteUser(email: string, groupId: number): void {
        this.inviting = true;
        this.http.post('/api/admin/invitations', { email, groupId }, { headers: this.adminService.getAuthHeaders() }).subscribe({
            next: () => {
                this.inviting = false;
                this.matSnackBar.open(`Invitation sent to ${email}`, 'Close', {
                    duration: 3000,
                    panelClass: ['success-snackbar']
                });
            },
            error: (error) => {
                this.inviting = false;
                this.matSnackBar.open(error.error?.message || 'Failed to send invitation', 'Close', {
                    duration: 3000,
                    panelClass: ['error-snackbar']
                });
            }
        });
    }

    openCreateUserDialog(): void {
        const dialogRef = this.matDialog.open(CreateUserDialogComponent, {
            width: '500px',
            data: { groups: this.availableGroups }
        });

        dialogRef.afterClosed().subscribe(result => {
            if (result) {
                this.createUser(result);
            }
        });
    }

    createUser(userData: any): void {
        this.saving = true;
        this.http.post('/api/admin/users/create', userData, { headers: this.adminService.getAuthHeaders() }).subscribe({
            next: (response: any) => {
                this.saving = false;
                this.matSnackBar.open(`User created successfully. PIN: ${response.pin}`, 'Close', {
                    duration: 10000,
                    panelClass: ['success-snackbar']
                });
                this.refreshUsers();
            },
            error: (error) => {
                this.saving = false;
                const message = error.error?.error || error.error?.message || 'Failed to create user';
                this.matSnackBar.open(message, 'Close', {
                    duration: 5000,
                    panelClass: ['error-snackbar']
                });
            }
        });
    }

    openResetPasswordDialog(user: User): void {
        const dialogRef = this.matDialog.open(ResetPasswordDialogComponent, {
            width: '450px',
            data: { userId: user.id, userEmail: user.email }
        });

        dialogRef.afterClosed().subscribe(result => {
            if (result) {
                this.resetUserPassword(user.id, result.newPassword);
            }
        });
    }

    resetUserPassword(userId: number, newPassword: string): void {
        this.saving = true;
        this.http.post(`/api/admin/users/${userId}/reset-password`, { newPassword }, { headers: this.adminService.getAuthHeaders() }).subscribe({
            next: () => {
                this.saving = false;
                this.matSnackBar.open('Password reset successfully', 'Close', {
                    duration: 3000,
                    panelClass: ['success-snackbar']
                });
            },
            error: (error) => {
                this.saving = false;
                const message = error.error?.error || error.error?.message || 'Failed to reset password';
                this.matSnackBar.open(message, 'Close', {
                    duration: 5000,
                    panelClass: ['error-snackbar']
                });
            }
        });
    }

    selectUserSystems(): void {
        if (!this.editingUser) {
            return;
        }

        try {
            // Build the form arrays for the dialog
            const groupsArray = this.fb.array(
                this.configGroups.map((group: any) => this.adminService.newGroupForm(group))
            );
            const tagsArray = this.fb.array(
                this.configTags.map((tag: any) => this.adminService.newTagForm(tag))
            );
            const systemsArray = this.fb.array(
                this.systems.map((system: any) => this.adminService.newSystemForm(system))
            );

            // Create the root form that contains groups, tags, systems, and the access form
            // This properly sets up the form hierarchy so accessForm.root points to rootForm
            const rootForm = this.fb.group({
                groups: groupsArray,
                tags: tagsArray,
                systems: systemsArray,
                access: this.fb.group({
                    systems: [this.editForm.get('systems')?.value || '*']
                })
            });

            // Get the access form from the root - now it has root property set correctly
            const accessForm = rootForm.get('access') as FormGroup;

            const matDialogRef = this.matDialog.open(RdioScannerAdminSystemsSelectComponent, { 
                data: accessForm,
                width: '90vw',
                maxWidth: '1200px',
                maxHeight: '90vh'
            });

            matDialogRef.afterClosed().subscribe((data) => {
                if (data !== null && data !== undefined) {
                    this.editForm.get('systems')?.setValue(data);
                    this.editForm.markAsDirty();
                }
            });
        } catch (error) {
            console.error('Error in selectUserSystems:', error);
            this.matSnackBar.open('Error opening system selection: ' + error, 'Close', {
                duration: 5000,
                panelClass: ['error-snackbar']
            });
        }
    }

    ngOnDestroy(): void {
        // Cleanup if needed
    }

    async loadUsers(forceReload: boolean = false): Promise<void> {
        // If form data is provided and not forcing reload, use it instead of loading from backend
        if (this.form && !forceReload) {
            this.updateUsersFromForm();
            return;
        }

        this.loading = true;
        this.error = null;

        try {
            const users = await this.adminService.getAllUsers();
            this.users = users;
            
            // Also update the parent form if it exists
            if (this.form) {
                this.form.clear();
                users.forEach((user: any) => {
                    this.form!.push(this.adminService.newUserForm(user));
                });
            }
            
            this.applyFilter(); // Apply filter and pagination
            this.loading = false;
            this.cdr.detectChanges(); // Trigger change detection
        } catch (error) {
            console.error('Failed to load users:', error);
            this.error = 'Failed to load users';
            this.loading = false;
            this.cdr.detectChanges(); // Trigger change detection
            this.matSnackBar.open('Failed to load users', 'Close', {
                duration: 3000,
                panelClass: ['error-snackbar']
            });
        }
    }

    async refreshUsers(): Promise<void> {
        await this.loadUsers(true);
    }

    async deleteUser(user: User): Promise<void> {
        if (!confirm(`Are you sure you want to delete user ${user.email}?`)) {
            return;
        }

        try {
            await this.adminService.deleteUser(user.id);
            this.matSnackBar.open(`User ${user.email} deleted successfully`, 'Close', {
                duration: 3000,
                panelClass: ['success-snackbar']
            });
            // Force reload from backend to remove deleted user and update parent form
            await this.loadUsers(true);
            // Mark parent form as dirty
            if (this.form && this.form.parent) {
                this.form.parent.markAsDirty();
            }
        } catch (error) {
            console.error('Failed to delete user:', error);
            this.matSnackBar.open('Failed to delete user', 'Close', {
                duration: 3000,
                panelClass: ['error-snackbar']
            });
        }
    }

    resendVerificationEmail(user: User): void {
        // Use fetch for this public API since it doesn't need admin authentication
        fetch('/api/user/resend-verification', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify({ email: user.email })
        }).then(response => {
            if (response.ok) {
                this.matSnackBar.open(`Verification email sent to ${user.email}`, 'Close', {
                    duration: 3000,
                    panelClass: ['success-snackbar']
                });
            } else {
                throw new Error('Failed to resend verification email');
            }
        }).catch(error => {
            console.error('Failed to resend verification email:', error);
            this.matSnackBar.open('Failed to resend verification email', 'Close', {
                duration: 3000,
                panelClass: ['error-snackbar']
            });
        });
    }

    // Editing methods
    startEdit(user: User): void {
        this.editingUser = user;
        this.regeneratePinRequested = false;
        
        // Convert epoch seconds to datetime-local format (YYYY-MM-DDTHH:mm)
        let pinExpiresAtValue = '';
        if (user.pinExpiresAt && user.pinExpiresAt > 0) {
            const date = new Date(user.pinExpiresAt * 1000);
            // Format as YYYY-MM-DDTHH:mm for datetime-local input
            pinExpiresAtValue = date.toISOString().slice(0, 16);
        }
        
        this.editForm.patchValue({
            email: user.email,
            firstName: user.firstName,
            lastName: user.lastName,
            zipCode: user.zipCode,
            verified: user.verified,
            systems: user.systems || '*',
            delay: user.delay,
            pin: user.pin,
            pinExpiresAt: pinExpiresAtValue,
            connectionLimit: user.connectionLimit || 0,
            systemDelays: user.systemDelays,
            talkgroupDelays: user.talkgroupDelays,
            userGroupId: user.userGroupId || 0,
            isGroupAdmin: user.isGroupAdmin || false,
            systemAdmin: user.systemAdmin || false,
            stripeCustomerId: user.stripeCustomerId || '',
            stripeSubscriptionId: user.stripeSubscriptionId || '',
            subscriptionStatus: user.subscriptionStatus || ''
        });

        // Parse system delays JSON
        this.systemDelayEntries = [];
        try {
            const systemDelaysMap = user.systemDelays ? JSON.parse(user.systemDelays) : {};
            this.systemDelayEntries = Object.keys(systemDelaysMap).map(key => ({
                systemId: parseInt(key),
                delay: systemDelaysMap[key]
            }));
        } catch {
            this.systemDelayEntries = [];
        }

        // Parse talkgroup delays JSON
        this.talkgroupDelayEntries = [];
        try {
            const talkgroupDelaysMap = user.talkgroupDelays ? JSON.parse(user.talkgroupDelays) : {};
            this.talkgroupDelayEntries = Object.keys(talkgroupDelaysMap).map(key => {
                const [systemId, talkgroupId] = key.split(':').map(Number);
                return {
                    systemId,
                    talkgroupId,
                    delay: talkgroupDelaysMap[key]
                };
            });
        } catch {
            this.talkgroupDelayEntries = [];
        }
    }

    cancelEdit(): void {
        this.editingUser = null;
        this.editForm.reset();
        this.regeneratePinRequested = false;
        this.systemDelayEntries = [];
        this.talkgroupDelayEntries = [];
    }

    async saveUser(): Promise<void> {
        if (!this.editingUser || this.editForm.invalid || this.saving) {
            return;
        }

        this.saving = true;
        const formValue = this.editForm.getRawValue();
        const userId = this.editingUser.id;

        const parseNonNegativeInt = (value: any): number => {
            const parsed = Number(value);
            if (!Number.isFinite(parsed) || parsed < 0) {
                return 0;
            }
            return Math.floor(parsed);
        };

        // Convert system delays to JSON map
        const systemDelaysMap: {[key: number]: number} = {};
        this.systemDelayEntries.forEach(entry => {
            if (entry.systemId && entry.delay >= 0) {
                systemDelaysMap[entry.systemId] = entry.delay;
            }
        });
        const systemDelaysJson = Object.keys(systemDelaysMap).length > 0
            ? JSON.stringify(systemDelaysMap)
            : '';

        // Convert talkgroup delays to JSON map
        const talkgroupDelaysMap: {[key: string]: number} = {};
        this.talkgroupDelayEntries.forEach(entry => {
            if (entry.systemId && entry.talkgroupId && entry.delay >= 0) {
                talkgroupDelaysMap[`${entry.systemId}:${entry.talkgroupId}`] = entry.delay;
            }
        });
        const talkgroupDelaysJson = Object.keys(talkgroupDelaysMap).length > 0
            ? JSON.stringify(talkgroupDelaysMap)
            : '';

        // Convert datetime-local string to epoch seconds
        let pinExpiresAtEpoch = 0;
        if (formValue.pinExpiresAt && formValue.pinExpiresAt.trim() !== '') {
            const date = new Date(formValue.pinExpiresAt);
            if (!isNaN(date.getTime())) {
                pinExpiresAtEpoch = Math.floor(date.getTime() / 1000);
            }
        }

        // Serialize systems if it's an object/array, otherwise keep as is
        let systemsValue = formValue.systems || '*';
        if (typeof systemsValue === 'object') {
            systemsValue = JSON.stringify(systemsValue);
        }

        const payload: any = {
            email: formValue.email,
            firstName: formValue.firstName,
            lastName: formValue.lastName,
            zipCode: formValue.zipCode,
            verified: !!formValue.verified,
            systems: systemsValue, // System and talkgroup access
            delay: parseNonNegativeInt(formValue.delay),
            systemDelays: systemDelaysJson,
            talkgroupDelays: talkgroupDelaysJson,
            connectionLimit: parseNonNegativeInt(formValue.connectionLimit),
            pinExpiresAt: pinExpiresAtEpoch,
            userGroupId: parseNonNegativeInt(formValue.userGroupId),
            isGroupAdmin: !!formValue.isGroupAdmin,
            systemAdmin: !!formValue.systemAdmin,
            stripeCustomerId: (formValue.stripeCustomerId ?? '').toString().trim(),
            stripeSubscriptionId: (formValue.stripeSubscriptionId ?? '').toString().trim(),
            subscriptionStatus: (formValue.subscriptionStatus ?? '').toString().trim(),
        };

        console.log('Saving user with payload:', payload);

        if (this.regeneratePinRequested) {
            payload.regeneratePin = true;
        } else if (formValue.pin !== undefined) {
            payload.pin = (formValue.pin ?? '').toString();
        }

        try {
            console.log('Calling updateUser with userId:', userId, 'payload:', payload);
            await this.adminService.updateUser(userId, payload);
            console.log('updateUser succeeded');
            await this.loadUsers(true);

            const updatedUser = this.users.find(u => u.id === userId);
            let message = 'User updated successfully';
            if ((this.regeneratePinRequested || (payload.pin !== undefined && payload.pin.trim() === '')) && updatedUser?.pin) {
                message += ` – new PIN: ${updatedUser.pin}`;
            }

            this.matSnackBar.open(message, 'Close', {
                duration: 4000,
                panelClass: ['success-snackbar']
            });

            this.cancelEdit();
            this.cdr.detectChanges();
        } catch (error: any) {
            console.error('Failed to update user:', error);
            const errorMessage = error?.error?.error || error?.error?.message || error?.message || 'Failed to update user';
            this.matSnackBar.open('Failed to update user: ' + errorMessage, 'Close', {
                duration: 5000,
                panelClass: ['error-snackbar']
            });
        } finally {
            this.saving = false;
            this.regeneratePinRequested = false;
        }
    }

    requestPinRegeneration(): void {
        this.regeneratePinRequested = true;
        this.editForm.get('pin')?.setValue('');
        this.matSnackBar.open('A new PIN will be generated when you save this user.', 'Close', {
            duration: 3000
        });
    }

    formatDate(dateString: string): string {
        if (!dateString) return 'Never';
        
        // Check if it's our custom "never logged in" message
        if (dateString === 'User has not logged in' || dateString === 'Never') {
            return dateString;
        }
        
        try {
            return new Date(dateString).toLocaleString();
        } catch {
            return 'Invalid date';
        }
    }

    getVerificationStatus(user: User): string {
        return user.verified ? 'Verified' : 'Pending';
    }

    getVerificationStatusColor(user: User): string {
        return user.verified ? 'primary' : 'warn';
    }

    formatPinExpiration(value: number): string {
        if (!value) {
            return 'No expiration';
        }

        return new Date(value * 1000).toLocaleString();
    }

    getConnectionLimitLabel(limit: number): string {
        if (!limit) {
            return 'Unlimited';
        }

        return `${limit} concurrent connection${limit === 1 ? '' : 's'}`;
    }

    getGroupName(userGroupId?: number): string | null {
        if (!userGroupId || userGroupId === 0) {
            return null;
        }
        const group = this.availableGroups.find(g => g.id === userGroupId);
        return group ? group.name : null;
    }

    openTransferDialog(user: User): void {
        const dialogRef = this.matDialog.open(TransferUserDialogComponent, {
            width: '500px',
            data: { 
                user: user,
                groups: this.availableGroups,
                currentGroupId: user.userGroupId || 0
            }
        });

        dialogRef.afterClosed().subscribe(result => {
            if (result && result.groupId) {
                this.transferUser(user.id, result.groupId);
            }
        });
    }

    transferUser(userId: number, toGroupId: number): void {
        this.http.post('/api/admin/users/transfer', 
            { userId, toGroupId }, 
            { headers: this.adminService.getAuthHeaders() }
        ).subscribe({
            next: () => {
                this.matSnackBar.open('User transferred successfully', 'Close', { duration: 3000 });
                this.loadUsers(true);
            },
            error: (error) => {
                console.error('Failed to transfer user:', error);
                this.matSnackBar.open(error.error?.message || 'Failed to transfer user', 'Close', { duration: 3000 });
            }
        });
    }

    // Search and pagination methods
    onSearchChange(): void {
        this.pageIndex = 0; // Reset to first page when search changes
        this.applyFilter();
    }

    clearSearch(): void {
        this.searchText = '';
        this.onSearchChange();
    }

    applyFilter(): void {
        // Filter users by name, email, or group
        if (this.searchText && this.searchText.trim() !== '') {
            const searchLower = this.searchText.toLowerCase();
            this.filteredUsers = this.users.filter(user => {
                const fullName = `${user.firstName} ${user.lastName}`.toLowerCase();
                const email = user.email.toLowerCase();
                const groupName = this.getGroupName(user.userGroupId)?.toLowerCase() || '';
                
                return fullName.includes(searchLower) || 
                       email.includes(searchLower) || 
                       groupName.includes(searchLower);
            });
        } else {
            this.filteredUsers = [...this.users];
        }

        // Apply pagination
        this.updatePaginatedUsers();
    }

    updatePaginatedUsers(): void {
        const startIndex = this.pageIndex * this.pageSize;
        const endIndex = startIndex + this.pageSize;
        this.paginatedUsers = this.filteredUsers.slice(startIndex, endIndex);
    }

    onPageChange(event: PageEvent): void {
        this.pageIndex = event.pageIndex;
        this.pageSize = event.pageSize;
        this.updatePaginatedUsers();
    }

    sendTestPush(user: User): void {
        this.http.post(`/api/admin/users/${user.id}/test-push`, {}, { headers: this.adminService.getAuthHeaders() }).subscribe({
            next: (response: any) => {
                this.matSnackBar.open(`Test push notification sent to ${user.email}`, 'Close', {
                    duration: 3000,
                    panelClass: ['success-snackbar']
                });
            },
            error: (error) => {
                console.error('Failed to send test push:', error);
                const message = error.error?.error || error.error?.message || 'Failed to send test push notification';
                this.matSnackBar.open(message, 'Close', {
                    duration: 5000,
                    panelClass: ['error-snackbar']
                });
            }
        });
    }
}

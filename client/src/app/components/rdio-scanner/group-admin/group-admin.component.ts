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

import { Component, OnInit, OnDestroy } from '@angular/core';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { Router } from '@angular/router';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTabChangeEvent } from '@angular/material/tabs';
import { Subscription } from 'rxjs';

interface GroupUser {
  id: number;
  email: string;
  firstName: string;
  lastName: string;
  verified: boolean;
  isGroupAdmin: boolean;
}

interface RegistrationCode {
  id: number;
  code: string;
  expiresAt: number;
  maxUses: number;
  currentUses: number;
  isOneTime: boolean;
  isActive: boolean;
  createdAt: number;
}

interface TransferRequest {
  id: number;
  userId: number;
  userEmail: string;
  fromGroupId: number;
  fromGroupName: string;
  toGroupId: number;
  toGroupName: string;
  status: string;
  requestedAt: number;
}

@Component({
    selector: 'rdio-scanner-group-admin',
    templateUrl: './group-admin.component.html',
    styleUrls: ['./group-admin.component.scss'],
    standalone: false
})
export class RdioScannerGroupAdminComponent implements OnInit, OnDestroy {
  selectedTab = 0;
  loading = false;
  
  // Users tab
  users: GroupUser[] = [];
  loadingUsers = false;
  maxUsers: number = 0;
  userCount: number = 0;
  inviteUserForm = {
    email: ''
  };
  invitingUser: boolean = false;
  addUserForm = {
    email: '',
    password: '',
    confirmPassword: '',
    firstName: '',
    lastName: '',
    zipCode: ''
  };
  addingUser: boolean = false;
  addExistingUserForm = {
    email: ''
  };
  addingExistingUser: boolean = false;
  
  // Codes tab
  codes: RegistrationCode[] = [];
  loadingCodes = false;
  newCodeForm = {
    expiresAt: 0,
    maxUses: 0,
    isOneTime: false
  };
  generatingCode = false;
  
  // Transfers tab
  transferRequests: TransferRequest[] = [];
  loadingTransfers = false;
  
  // Available groups for transfers
  availableGroups: any[] = [];
  transferringUser: number | null = null;
  selectedTransferGroupId: number = 0;

  userInfo: any;
  groupInfo: any;
  private pin: string = '';

  constructor(
    private http: HttpClient,
    private router: Router,
    private snackBar: MatSnackBar
  ) {}

  ngOnInit(): void {
    // Check if user is logged in
    const userStr = sessionStorage.getItem('groupAdminUser');
    const groupStr = sessionStorage.getItem('groupAdminGroup');
    
    if (!userStr || !groupStr) {
      this.router.navigate(['/group-admin/login']);
      return;
    }

    this.userInfo = JSON.parse(userStr);
    this.groupInfo = JSON.parse(groupStr);
    
    // Get PIN from localStorage - prefer groupAdminPin, fallback to userPin
    this.pin = localStorage.getItem('groupAdminPin') || localStorage.getItem('userPin') || '';
    
    if (!this.pin) {
      // If no PIN found, redirect to login
      this.router.navigate(['/group-admin/login']);
      return;
    }
    
    this.loadUsers();
    this.loadCodes();
    this.loadTransferRequests();
    this.loadAvailableGroups();
  }
  
  loadAvailableGroups(): void {
    this.http.get('/api/group-admin/available-groups', { headers: this.getHeaders() }).subscribe({
      next: (response: any) => {
        this.availableGroups = response.groups || [];
      },
      error: (error) => {
        console.error('Failed to load groups:', error);
        this.availableGroups = [];
      }
    });
  }

  ngOnDestroy(): void {}

  private getHeaders(): HttpHeaders {
    return new HttpHeaders({
      'Authorization': `Bearer ${this.pin}`
    });
  }

  loadUsers(): void {
    this.loadingUsers = true;
    this.http.get('/api/group-admin/users', { headers: this.getHeaders() }).subscribe({
      next: (response: any) => {
        this.loadingUsers = false;
        this.users = response.users || [];
        if (response.group) {
          this.maxUsers = response.group.maxUsers || 0;
          this.userCount = response.group.userCount || 0;
          // Update groupInfo to keep it in sync
          if (this.groupInfo) {
            this.groupInfo.maxUsers = this.maxUsers;
            this.groupInfo.userCount = this.userCount;
            if (response.group.allowAddExistingUsers !== undefined) {
              this.groupInfo.allowAddExistingUsers = response.group.allowAddExistingUsers;
            }
          }
        }
      },
      error: (error) => {
        this.loadingUsers = false;
        this.snackBar.open('Failed to load users', 'Close', { duration: 3000 });
      }
    });
  }

  loadCodes(): void {
    this.loadingCodes = true;
    this.http.get('/api/group-admin/codes', { headers: this.getHeaders() }).subscribe({
      next: (response: any) => {
        this.loadingCodes = false;
        this.codes = response.codes || [];
      },
      error: (error) => {
        this.loadingCodes = false;
        this.snackBar.open('Failed to load codes', 'Close', { duration: 3000 });
      }
    });
  }

  loadTransferRequests(): void {
    this.loadingTransfers = true;
    this.http.get('/api/group-admin/transfer-requests', { headers: this.getHeaders() }).subscribe({
      next: (response: any) => {
        this.loadingTransfers = false;
        this.transferRequests = response.requests || [];
      },
      error: (error) => {
        this.loadingTransfers = false;
        this.snackBar.open('Failed to load transfer requests', 'Close', { duration: 3000 });
      }
    });
  }

  onTabChange(event: MatTabChangeEvent): void {
    this.selectedTab = event.index;
    if (this.selectedTab === 0) {
      this.loadUsers();
    } else if (this.selectedTab === 1) {
      this.loadCodes();
    } else if (this.selectedTab === 2) {
      this.loadTransferRequests();
    }
  }

  inviteUser(): void {
    // Validate email
    if (!this.inviteUserForm.email || !this.inviteUserForm.email.trim()) {
      this.snackBar.open('Email is required', 'Close', { duration: 3000 });
      return;
    }

    // Check if at max users
    if (this.maxUsers > 0 && this.userCount >= this.maxUsers) {
      this.snackBar.open(`Group has reached maximum user limit of ${this.maxUsers}`, 'Close', { duration: 3000 });
      return;
    }

    this.invitingUser = true;
    const payload = {
      email: this.inviteUserForm.email.trim()
    };

    this.http.post('/api/group-admin/invite-user', payload, { headers: this.getHeaders() }).subscribe({
      next: (response: any) => {
        this.invitingUser = false;
        this.inviteUserForm = {
          email: ''
        };
        this.snackBar.open(`Invitation sent to ${payload.email}`, 'Close', { duration: 5000 });
        // Note: User count doesn't change until they register
      },
      error: (error) => {
        this.invitingUser = false;
        this.snackBar.open(error.error?.message || 'Failed to send invitation', 'Close', { duration: 3000 });
      }
    });
  }

  addUser(): void {
    // Validate required fields
    if (!this.addUserForm.email || !this.addUserForm.email.trim()) {
      this.snackBar.open('Email is required', 'Close', { duration: 3000 });
      return;
    }
    if (!this.addUserForm.firstName || !this.addUserForm.firstName.trim()) {
      this.snackBar.open('First name is required', 'Close', { duration: 3000 });
      return;
    }
    if (!this.addUserForm.lastName || !this.addUserForm.lastName.trim()) {
      this.snackBar.open('Last name is required', 'Close', { duration: 3000 });
      return;
    }
    if (!this.addUserForm.zipCode || !this.addUserForm.zipCode.trim()) {
      this.snackBar.open('ZIP code is required', 'Close', { duration: 3000 });
      return;
    }
    if (!this.addUserForm.password || this.addUserForm.password.length < 6) {
      this.snackBar.open('Password is required and must be at least 6 characters', 'Close', { duration: 3000 });
      return;
    }
    if (this.addUserForm.password !== this.addUserForm.confirmPassword) {
      this.snackBar.open('Passwords do not match', 'Close', { duration: 3000 });
      return;
    }

    // Check if at max users
    if (this.maxUsers > 0 && this.userCount >= this.maxUsers) {
      this.snackBar.open(`Group has reached maximum user limit of ${this.maxUsers}`, 'Close', { duration: 3000 });
      return;
    }

    this.addingUser = true;
    const payload = {
      email: this.addUserForm.email.trim(),
      password: this.addUserForm.password,
      firstName: this.addUserForm.firstName.trim(),
      lastName: this.addUserForm.lastName.trim(),
      zipCode: this.addUserForm.zipCode.trim()
    };

    this.http.post('/api/group-admin/add-user', payload, { headers: this.getHeaders() }).subscribe({
      next: () => {
        this.addingUser = false;
        this.addUserForm = {
          email: '',
          password: '',
          confirmPassword: '',
          firstName: '',
          lastName: '',
          zipCode: ''
        };
        this.snackBar.open('User added successfully', 'Close', { duration: 3000 });
        this.loadUsers();
      },
      error: (error) => {
        this.addingUser = false;
        this.snackBar.open(error.error?.message || 'Failed to add user', 'Close', { duration: 3000 });
      }
    });
  }

  addExistingUser(): void {
    if (!this.addExistingUserForm.email || !this.addExistingUserForm.email.trim()) {
      this.snackBar.open('Email is required', 'Close', { duration: 3000 });
      return;
    }

    // Check if at max users
    if (this.maxUsers > 0 && this.userCount >= this.maxUsers) {
      this.snackBar.open(`Group has reached maximum user limit of ${this.maxUsers}`, 'Close', { duration: 3000 });
      return;
    }

    this.addingExistingUser = true;
    const payload = {
      email: this.addExistingUserForm.email.trim()
    };

    this.http.post('/api/group-admin/add-existing-user', payload, { headers: this.getHeaders() }).subscribe({
      next: (response: any) => {
        this.addingExistingUser = false;
        this.addExistingUserForm.email = '';
        this.snackBar.open(response.message || 'User added to group successfully', 'Close', { duration: 3000 });
        this.loadUsers();
      },
      error: (error) => {
        this.addingExistingUser = false;
        this.snackBar.open(error.error?.message || 'Failed to add existing user', 'Close', { duration: 3000 });
      }
    });
  }

  toggleGroupAdmin(userId: number, isGroupAdmin: boolean): void {
    const action = isGroupAdmin ? 'promote this user to group admin' : 'demote this user from group admin';
    if (!confirm(`Are you sure you want to ${action}?`)) {
      return;
    }

    this.http.post('/api/group-admin/toggle-admin', { userId, isGroupAdmin }, { headers: this.getHeaders() }).subscribe({
      next: () => {
        const message = isGroupAdmin ? 'User promoted to group admin' : 'User demoted from group admin';
        this.snackBar.open(message, 'Close', { duration: 3000 });
        this.loadUsers();
      },
      error: (error) => {
        this.snackBar.open(error.error?.message || 'Failed to update user status', 'Close', { duration: 3000 });
      }
    });
  }


  generateCode(): void {
    this.generatingCode = true;
    const payload = {
      expiresAt: this.newCodeForm.expiresAt > 0 ? this.newCodeForm.expiresAt : 0,
      maxUses: this.newCodeForm.maxUses > 0 ? this.newCodeForm.maxUses : 0,
      isOneTime: this.newCodeForm.isOneTime
    };

    this.http.post('/api/group-admin/generate-code', payload, { headers: this.getHeaders() }).subscribe({
      next: (response: any) => {
        this.generatingCode = false;
        this.snackBar.open(`Code generated: ${response.code}`, 'Close', { duration: 5000 });
        this.newCodeForm = { expiresAt: 0, maxUses: 0, isOneTime: false };
        this.loadCodes();
      },
      error: (error) => {
        this.generatingCode = false;
        this.snackBar.open(error.error?.message || 'Failed to generate code', 'Close', { duration: 3000 });
      }
    });
  }

  openTransferDialog(userId: number): void {
    this.transferringUser = userId;
    this.selectedTransferGroupId = 0;
  }
  
  cancelTransfer(): void {
    this.transferringUser = null;
    this.selectedTransferGroupId = 0;
  }
  
  confirmTransfer(): void {
    if (!this.transferringUser || !this.selectedTransferGroupId) {
      this.snackBar.open('Please select a target group', 'Close', { duration: 3000 });
      return;
    }
    
    this.requestTransfer(this.transferringUser, this.selectedTransferGroupId);
    this.cancelTransfer();
  }

  requestTransfer(userId: number, toGroupId: number): void {
    this.http.post('/api/group-admin/request-transfer', 
      { userId, toGroupId }, 
      { headers: this.getHeaders() }
    ).subscribe({
      next: (response: any) => {
        const message = response.message || 'Transfer request created';
        this.snackBar.open(message, 'Close', { duration: 3000 });
        this.loadTransferRequests();
        this.loadUsers(); // Refresh users in case transfer was auto-approved
      },
      error: (error) => {
        this.snackBar.open(error.error?.message || 'Failed to create transfer request', 'Close', { duration: 3000 });
      }
    });
  }

  approveTransfer(requestId: number, approve: boolean): void {
    this.http.post('/api/group-admin/approve-transfer', 
      { requestId, approve }, 
      { headers: this.getHeaders() }
    ).subscribe({
      next: () => {
        this.snackBar.open(approve ? 'Transfer approved' : 'Transfer rejected', 'Close', { duration: 3000 });
        this.loadTransferRequests();
        this.loadUsers();
      },
      error: (error) => {
        this.snackBar.open(error.error?.message || 'Failed to update transfer request', 'Close', { duration: 3000 });
      }
    });
  }

  logout(): void {
    sessionStorage.removeItem('groupAdminUser');
    sessionStorage.removeItem('groupAdminGroup');
    localStorage.removeItem('groupAdminPin');
    this.router.navigate(['/']);
  }

  formatDate(timestamp: number): string {
    if (!timestamp || timestamp === 0) return 'Never';
    return new Date(timestamp * 1000).toLocaleString();
  }

  deleteCode(codeId: number): void {
    if (!confirm('Are you sure you want to delete this registration code? This action cannot be undone.')) {
      return;
    }

    this.http.delete(`/api/group-admin/codes/${codeId}`, { headers: this.getHeaders() }).subscribe({
      next: () => {
        this.snackBar.open('Registration code deleted successfully', 'Close', { duration: 3000 });
        this.loadCodes();
      },
      error: (error) => {
        console.error('Failed to delete code:', error);
        this.snackBar.open(error.error?.message || 'Failed to delete registration code', 'Close', { duration: 3000 });
      }
    });
  }
}


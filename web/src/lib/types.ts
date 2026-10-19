// Types mirroring the JSON of the Homelydrive server API.

export interface Me {
  id: number
  username: string
  display_name: string
  name: string
  is_admin: boolean
  quota: number
  used: number
  lang: 'de' | 'en'
  has_vault: boolean
  avatar: number
}

export interface FileInfo {
  id: number
  name: string
  path: string
  space?: string
  space_name?: string
  is_dir: boolean
  size: number
  mtime: number
  mime?: string
  category?: string
  viewer?: string
  sha256?: string
  starred?: boolean
  color?: string
  note?: string
  tags?: string[]
  shared?: boolean
  can_write: boolean
  taken_at?: number
}

export interface ListResult {
  space: string
  space_id: number
  base: string
  can_write: boolean
  can_share: boolean
  label: string
  entries: FileInfo[]
}

export interface Root {
  ref: string
  kind: 'home' | 'team' | 'mount' | 'share'
  name: string
  can_write: boolean
  is_dir: boolean
  owner?: string
  space_id: number
  path?: string
}

export interface TrashItem {
  id: number
  name: string
  path: string
  space: string
  space_name?: string
  is_dir: boolean
  size: number
  deleted_by?: string
  deleted_at: number
  expires_at: number
}

export interface Share {
  id: number
  space?: string
  path: string
  name: string
  is_dir: boolean
  target_id: number
  target_name: string
  target_avatar?: number
  owner_name?: string
  can_write: boolean
  created_at: number
}

export interface Link {
  id: number
  token: string
  kind: 'download' | 'upload'
  space?: string
  path: string
  name: string
  is_dir: boolean
  has_password: boolean
  expires_at: number
  max_downloads: number
  downloads: number
  created_at: number
}

export interface Notification {
  id: number
  kind: string
  data: Record<string, any>
  read: boolean
  created_at: number
}

export interface Activity {
  id: number
  actor: string
  actor_id?: number
  actor_avatar?: number
  action: string
  space?: string
  path: string
  details?: string
  created_at: number
}

export interface FileType {
  ext: string
  mime: string
  category: string
  viewer: string
  auto: boolean
  builtin: boolean
}

export interface VaultItem {
  id: number
  parent_id: number
  name: string
  is_dir: boolean
  size: number
  mtime: number
  category?: string
  viewer?: string
  mime?: string
}

export interface StorageStats {
  used: number
  quota: number
  percent: number
  files: number
  trash: number
  vault: number
  by_category: Record<string, number>
  largest: FileInfo[]
  disk_free: number
  disk_total: number
}

export interface UserBrief {
  id: number
  username: string
  display_name: string
  avatar?: number
}

export interface Device {
  id: number
  device: string
  created_at: number
  last_used_at: number
  current: boolean
}

export interface Release {
  version: string
  url: string
  newer: boolean
}

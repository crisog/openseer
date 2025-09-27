create table "user" (
  "id" text not null primary key,
  "name" text not null,
  "email" text not null unique,
  "emailVerified" boolean not null,
  "image" text,
  "createdAt" timestamp default CURRENT_TIMESTAMP not null,
  "updatedAt" timestamp default CURRENT_TIMESTAMP not null
);

create table "session" (
  "id" text not null primary key,
  "expiresAt" timestamp not null,
  "token" text not null unique,
  "createdAt" timestamp default CURRENT_TIMESTAMP not null,
  "updatedAt" timestamp not null,
  "ipAddress" text,
  "userAgent" text,
  "userId" text not null references "user" ("id") on delete cascade,
  "activeOrganizationId" text,
  "activeTeamId" text
);

create table "account" (
  "id" text not null primary key,
  "accountId" text not null,
  "providerId" text not null,
  "userId" text not null references "user" ("id") on delete cascade,
  "accessToken" text,
  "refreshToken" text,
  "idToken" text,
  "accessTokenExpiresAt" timestamp,
  "refreshTokenExpiresAt" timestamp,
  "scope" text,
  "password" text,
  "createdAt" timestamp default CURRENT_TIMESTAMP not null,
  "updatedAt" timestamp not null
);

create table "verification" (
  "id" text not null primary key,
  "identifier" text not null,
  "value" text not null,
  "expiresAt" timestamp not null,
  "createdAt" timestamp default CURRENT_TIMESTAMP not null,
  "updatedAt" timestamp default CURRENT_TIMESTAMP not null
);

create table "organization" (
  "id" text not null primary key,
  "name" text not null,
  "slug" text not null unique,
  "logo" text,
  "metadata" text,
  "createdAt" timestamp default CURRENT_TIMESTAMP not null
);

create table "member" (
  "id" text not null primary key,
  "organizationId" text not null references "organization" ("id") on delete cascade,
  "userId" text not null references "user" ("id") on delete cascade,
  "role" text not null default 'member',
  "createdAt" timestamp default CURRENT_TIMESTAMP not null
);

create index "member_organizationId_idx" on "member" ("organizationId");
create index "member_userId_idx" on "member" ("userId");

create table "invitation" (
  "id" text not null primary key,
  "organizationId" text not null references "organization" ("id") on delete cascade,
  "email" text not null,
  "role" text,
  "teamId" text,
  "status" text not null default 'pending',
  "expiresAt" timestamp not null,
  "inviterId" text not null references "user" ("id") on delete cascade,
  "createdAt" timestamp default CURRENT_TIMESTAMP not null,
  "updatedAt" timestamp default CURRENT_TIMESTAMP not null
);

create index "invitation_organizationId_idx" on "invitation" ("organizationId");
create index "invitation_email_idx" on "invitation" ("email");

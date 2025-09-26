import authClient from "@/lib/auth-client";
import { userClient } from "@/lib/api/connect-client";
import { create } from "@bufbuild/protobuf";
import { GetUserProfileRequestSchema } from "@/lib/gen/openseer/v1/user_pb";

export interface SignInData {
  email: string;
  password: string;
}

export interface SignUpData {
  email: string;
  password: string;
  name: string;
}

export interface UserProfile {
  id: string;
  email: string;
  name: string;
}

export const authApi = {
  signIn: async ({ email, password }: SignInData) => {
    const { data, error } = await authClient.signIn.email({
      email,
      password,
    });

    if (error) {
      throw new Error(error.message || "Failed to sign in");
    }

    return data;
  },

  signUp: async ({ email, password, name }: SignUpData) => {
    const { data, error } = await authClient.signUp.email({
      email,
      password,
      name,
    });

    if (error) {
      throw new Error(error.message || "Failed to create account");
    }

    return data;
  },

  getSession: async () => {
    const session = await authClient.getSession();
    return session.data;
  },

  fetchUserProfile: async (): Promise<UserProfile> => {
    try {
      const request = create(GetUserProfileRequestSchema);
      const response = await userClient.getUserProfile(request);
      
      if (!response.user) {
        throw new Error("User data not found in response");
      }

      return {
        id: response.user.id,
        email: response.user.email,
        name: response.user.name,
      };
    } catch (error) {
      if (error instanceof Error) {
        throw new Error(`Backend server error: ${error.message}`);
      }
      throw new Error("Failed to fetch user profile");
    }
  },
};
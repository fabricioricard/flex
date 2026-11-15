// Import the functions you need from the SDKs you need
import { initializeApp } from "firebase/app";
import { getAnalytics } from "firebase/analytics";

// Your web app's Firebase configuration
const firebaseConfig = {
  apiKey: "AIzaSyARu6cNGcMpupGnwD2AAdbJPTy4tJwecMk",
  authDomain: "flex-6b79c.firebaseapp.com",
  projectId: "flex-6b79c",
  storageBucket: "flex-6b79c.firebasestorage.app",
  messagingSenderId: "142605637768",
  appId: "1:142605637768:web:57fedf126b4424c3a4673d",
  measurementId: "G-4J8VY91P31"
};

// Initialize Firebase
const app = initializeApp(firebaseConfig);
const analytics = getAnalytics(app);

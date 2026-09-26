import React from 'react';
import {createRoot} from 'react-dom/client';
import {QueryClient,QueryClientProvider} from '@tanstack/react-query';
import {HttpError} from '@oazapfts/runtime';
import App from './App';
import * as api from './api';
import './style.css';
api.defaults.baseUrl='';
api.defaults.credentials='same-origin';
const busy=(error:unknown):error is HttpError=>error instanceof HttpError&&error.status===503&&error.data?.code==='busy';
const retry=(failures:number,error:unknown)=>failures<(busy(error)?4:1);
const retryDelay=(attempt:number,error:unknown)=>{
 if(!busy(error))return Math.min(1000*2**attempt,30000);
 const seconds=Number(error.headers.get('Retry-After')??1);
 const minimum=Number.isFinite(seconds)&&seconds>=0&&seconds<=60?Math.max(1000,seconds*1000):1000;
 return Math.max(minimum,Math.min(1000*2**attempt,8000))+Math.random()*500;
};
export const queries=new QueryClient({defaultOptions:{queries:{retry,retryDelay,staleTime:20000,refetchOnWindowFocus:false}}});
createRoot(document.getElementById('root')!).render(<React.StrictMode><QueryClientProvider client={queries}><App/></QueryClientProvider></React.StrictMode>);

#include "expression.hpp"
#include <chrono>
#include <iomanip>
#include <iostream>
int main(int argc,char** argv){
 if(argc<2 || argc%2!=0) return 2;
 try {
  rules::Variables variables;
  for(int i=2;i<argc;i+=2){std::size_t used=0;std::string s(argv[i+1]);double value=std::stod(s,&used);if(used!=s.size()||!std::isfinite(value))return 2;variables[argv[i]]=value;}
  auto start=std::chrono::steady_clock::now();double value=rules::Parser(argv[1]).compile()->evaluate(variables);
  auto ns=std::chrono::duration_cast<std::chrono::nanoseconds>(std::chrono::steady_clock::now()-start).count();
  std::cout<<std::setprecision(17)<<value<<" "<<ns<<"\n";
  return 0;
 } catch(const std::exception&){return 2;}
}
